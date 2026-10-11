package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/apikey"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"sort"
	"time"
	"unicode/utf8"

	bolt "go.etcd.io/bbolt"
)

const (
	maxAPIKeyLabels     = 10_000
	MaxAPIKeyLabelRunes = 120
)

func (s *Store) APIKeyLabels() (map[string]string, error) {
	resp := make(chan labelQueryResult, 1)
	if err := s.send(labelQueryCommand{resp: resp}); err != nil {
		return nil, err
	}
	result := <-resp
	return result.labels, result.err
}

func (s *Store) SetAPIKeyLabel(hash, label string) error {
	resp := make(chan error, 1)
	if err := s.send(labelSetCommand{hash: hash, label: label, resp: resp}); err != nil {
		return err
	}
	return <-resp
}

func (s *Store) ResolveAPIKeyHash(hash string) (string, error) {
	resp := make(chan apiKeyResolveResult, 1)
	if err := s.send(apiKeyResolveCommand{hash: hash, resp: resp}); err != nil {
		return "", err
	}
	result := <-resp
	return result.ref, result.err
}

func (s *Store) APIKeyCryptoState() (uint64, map[uint64]apikey.APIKeyCryptoGeneration) {
	s.cryptoMu.RLock()
	defer s.cryptoMu.RUnlock()
	return s.activeGeneration, cloneAPIKeyGenerations(s.generations)
}

func databaseHasAPIKeyData(hours, requests *bolt.Bucket) (bool, error) {
	if hours != nil {
		found := false
		err := hours.ForEach(func(hourKey, value []byte) error {
			if found || value != nil {
				return nil
			}
			hour := hours.Bucket(hourKey)
			if hour == nil {
				return nil
			}
			return hour.ForEach(func(key, value []byte) error {
				if value == nil {
					return nil
				}
				var dimensions Dimensions
				if err := json.Unmarshal(key, &dimensions); err != nil {
					return fmt.Errorf("decode dimensions while checking crypto identity: %w", err)
				}
				if dimensions.APIKey != "" || dimensions.APIKeyHash != "" {
					found = true
				}
				return nil
			})
		})
		if err != nil || found {
			return found, err
		}
	}
	if requests != nil {
		found := false
		err := requests.ForEach(func(_, value []byte) error {
			if found || value == nil {
				return nil
			}
			var request RequestDetail
			if err := json.Unmarshal(value, &request); err != nil {
				return fmt.Errorf("decode request while checking crypto identity: %w", err)
			}
			if request.APIKey != "" || request.APIKeyHash != "" {
				found = true
			}
			return nil
		})
		return found, err
	}
	return false, nil
}

func cloneAPIKeyGenerations(values map[uint64]apikey.APIKeyCryptoGeneration) map[uint64]apikey.APIKeyCryptoGeneration {
	cloned := make(map[uint64]apikey.APIKeyCryptoGeneration, len(values))
	for id, generation := range values {
		cloned[id] = generation
	}
	return cloned
}

func validAPIKeyKeyID(value string) bool {
	return apikey.ValidHash(value)
}

func loadAPIKeyGenerations(meta *bolt.Bucket) (map[uint64]apikey.APIKeyCryptoGeneration, error) {
	generations := make(map[uint64]apikey.APIKeyCryptoGeneration)
	identities := make(map[string]uint64)
	raw := meta.Get(apiKeyGenerationsKey)
	if len(raw) == 0 {
		return generations, nil
	}
	var stored []apikey.APIKeyCryptoGeneration
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("decode API key crypto generations: %w", err)
	}
	for _, generation := range stored {
		if generation.ID == 0 {
			return nil, errors.New("API key crypto generation ID must not be zero")
		}
		if _, duplicate := generations[generation.ID]; duplicate {
			return nil, fmt.Errorf("duplicate API key crypto generation %d", generation.ID)
		}
		if generation.IdentityMissing {
			if generation.KeyID != "" || generation.HashVersion != "" {
				return nil, fmt.Errorf("API key crypto generation %d has inconsistent missing identity", generation.ID)
			}
		} else if !validAPIKeyKeyID(generation.KeyID) || generation.HashVersion == "" {
			return nil, fmt.Errorf("API key crypto generation %d is invalid", generation.ID)
		}
		if !generation.IdentityMissing {
			identity := generation.KeyID + "\x00" + generation.HashVersion
			if existing := identities[identity]; existing != 0 {
				return nil, fmt.Errorf("API key crypto generations %d and %d have duplicate identities", existing, generation.ID)
			}
			identities[identity] = generation.ID
		}
		generations[generation.ID] = generation
	}
	return generations, nil
}

func saveAPIKeyGenerations(meta *bolt.Bucket, generations map[uint64]apikey.APIKeyCryptoGeneration) error {
	values := make([]apikey.APIKeyCryptoGeneration, 0, len(generations))
	for _, generation := range generations {
		values = append(values, generation)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	encoded, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encode API key crypto generations: %w", err)
	}
	if err := meta.Put(apiKeyGenerationsKey, encoded); err != nil {
		return err
	}
	return nil
}

func findAPIKeyGeneration(generations map[uint64]apikey.APIKeyCryptoGeneration, crypto apikey.CryptoContext) uint64 {
	if !crypto.Enabled {
		return 0
	}
	for id, generation := range generations {
		if !generation.IdentityMissing && generation.KeyID == crypto.KeyID && generation.HashVersion == apikey.HashVersion {
			return id
		}
	}
	return 0
}

func activateAPIKeyGeneration(meta *bolt.Bucket, generations map[uint64]apikey.APIKeyCryptoGeneration, crypto apikey.CryptoContext, now time.Time) (uint64, map[uint64]apikey.APIKeyCryptoGeneration, error) {
	rawNext := meta.Get(apiKeyNextGenerationKey)
	if len(rawNext) != 0 && len(rawNext) != 8 {
		return 0, nil, errors.New("invalid API key crypto generation sequence metadata")
	}
	if !crypto.Enabled {
		return 0, generations, nil
	}
	if id := findAPIKeyGeneration(generations, crypto); id != 0 {
		return id, generations, nil
	}
	next := decodeUint64(rawNext)
	if next == 0 {
		next = 1
		for id := range generations {
			if id == ^uint64(0) {
				return 0, nil, errors.New("API key crypto generation sequence exhausted")
			}
			if id >= next {
				next = id + 1
			}
		}
	} else {
		for id := range generations {
			if id >= next {
				return 0, nil, fmt.Errorf("API key next generation %d does not follow existing generation %d", next, id)
			}
		}
	}
	if next == 0 || next == ^uint64(0) {
		return 0, nil, errors.New("API key crypto generation sequence exhausted")
	}
	updated := cloneAPIKeyGenerations(generations)
	updated[next] = apikey.APIKeyCryptoGeneration{ID: next, KeyID: crypto.KeyID, HashVersion: apikey.HashVersion, CreatedAt: now.UTC()}
	if err := saveAPIKeyGenerations(meta, updated); err != nil {
		return 0, nil, err
	}
	if err := meta.Put(apiKeyNextGenerationKey, encodeUint64(next+1)); err != nil {
		return 0, nil, err
	}
	return next, updated, nil
}

func ValidateAPIKeyLabel(ref, label string) error {
	if _, _, ok := apikey.ParseRef(ref); !ok {
		return errors.New("api key ref must identify a valid crypto generation and fingerprint")
	}
	if !utf8.ValidString(label) {
		return errors.New("api key label must be valid UTF-8")
	}
	if utf8.RuneCountInString(label) > MaxAPIKeyLabelRunes {
		return fmt.Errorf("api key label must not exceed %d characters", MaxAPIKeyLabelRunes)
	}
	return nil
}

func validateAPIKeyLabels(labels map[string]string) error {
	if len(labels) > maxAPIKeyLabels {
		return fmt.Errorf("api key labels exceed limit %d", maxAPIKeyLabels)
	}
	for ref, label := range labels {
		if label == "" {
			return errors.New("stored api key labels must not be empty")
		}
		if err := ValidateAPIKeyLabel(ref, label); err != nil {
			return err
		}
	}
	return nil
}

func (a *storeActor) retainedAPIKeyRefs() map[string]struct{} {
	refs := make(map[string]struct{})
	for key := range a.data {
		if ref := apikey.Ref(key.Dimensions.APIKeyGeneration, key.Dimensions.APIKeyHash); ref != "" {
			refs[ref] = struct{}{}
		}
	}
	for _, request := range a.pendingRequests {
		if ref := apikey.Ref(request.APIKeyGeneration, request.APIKeyHash); ref != "" {
			refs[ref] = struct{}{}
		}
	}
	return refs
}

func (a *storeActor) hasRetainedAPIKeyRef(ref string) bool {
	_, exists := a.retainedAPIKeyRefs()[ref]
	return exists
}

func (a *storeActor) resolveAPIKeyHash(hash string) (string, error) {
	if !apikey.ValidHash(hash) {
		return "", errs.WithStatus(400, "api_key_hash must be 32 lowercase hexadecimal characters")
	}
	var match string
	for ref := range a.retainedAPIKeyRefs() {
		_, candidate, _ := apikey.ParseRef(ref)
		if candidate != hash {
			continue
		}
		if match != "" && match != ref {
			return "", errs.WithStatus(400, "api_key_hash matches multiple crypto generations; use api_key_ref")
		}
		match = ref
	}
	if match == "" {
		return "", errs.WithStatus(404, "api_key_hash is not present in retained data")
	}
	return match, nil
}

func (a *storeActor) saveAPIKeyLabels(labels map[string]string) error {
	if err := validateAPIKeyLabels(labels); err != nil {
		return err
	}
	encoded, err := json.Marshal(labels)
	if err != nil {
		return fmt.Errorf("encode API key labels: %w", err)
	}
	if err := a.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if meta == nil {
			return errors.New("metadata bucket is missing")
		}
		if len(labels) == 0 {
			return meta.Delete(apiKeyLabelsKey)
		}
		return meta.Put(apiKeyLabelsKey, encoded)
	}); err != nil {
		return fmt.Errorf("save API key labels: %w", err)
	}
	return nil
}

func (a *storeActor) setAPIKeyLabel(ref, label string) error {
	if err := ValidateAPIKeyLabel(ref, label); err != nil {
		return errs.WithStatus(400, "%v", err)
	}
	candidate := cloneStringMap(a.apiKeyLabels)
	if label == "" {
		delete(candidate, ref)
	} else {
		if !a.hasRetainedAPIKeyRef(ref) {
			return errs.WithStatus(404, "api key ref is not present in retained data")
		}
		if len(candidate) >= maxAPIKeyLabels {
			if _, replacing := candidate[ref]; !replacing {
				return errs.WithStatus(409, "api key label limit reached")
			}
		}
		candidate[ref] = label
	}
	if err := a.saveAPIKeyLabels(candidate); err != nil {
		return err
	}
	a.apiKeyLabels = candidate
	return nil
}

func retainedAPIKeyState(hours, requests *bolt.Bucket) (map[string]struct{}, map[string]string, error) {
	hashes := make(map[string]struct{})
	ciphertexts := make(map[string]string)
	if err := hours.ForEach(func(hourKey, value []byte) error {
		if value != nil {
			return nil
		}
		hour := hours.Bucket(hourKey)
		if hour == nil {
			return nil
		}
		return hour.ForEach(func(key, value []byte) error {
			if value == nil {
				return nil
			}
			var dimensions Dimensions
			if err := json.Unmarshal(key, &dimensions); err != nil {
				return fmt.Errorf("decode dimensions while pruning API key state: %w", err)
			}
			if ref := apikey.Ref(dimensions.APIKeyGeneration, dimensions.APIKeyHash); ref != "" {
				hashes[ref] = struct{}{}
			}
			return nil
		})
	}); err != nil {
		return nil, nil, err
	}
	if err := requests.ForEach(func(_, value []byte) error {
		if value == nil {
			return errors.New("request bucket contains nested bucket")
		}
		var request RequestDetail
		if err := json.Unmarshal(value, &request); err != nil {
			return fmt.Errorf("decode request while pruning API key state: %w", err)
		}
		if ref := apikey.Ref(request.APIKeyGeneration, request.APIKeyHash); ref != "" {
			hashes[ref] = struct{}{}
			if request.APIKey != "" {
				ciphertexts[ref] = request.APIKey
			}
		}
		return nil
	}); err != nil {
		return nil, nil, err
	}
	return hashes, ciphertexts, nil
}
