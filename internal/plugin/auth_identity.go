package plugin

import (
	"crypto/sha256"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/store"
	"strings"
	"sync"
	"time"
)

const (
	authIdentitySuccessTTL = 10 * time.Minute
	authIdentityFailureTTL = time.Minute
)

// authRuntimeMetadata intentionally contains only presentation metadata from
// host.auth.get_runtime. Credential JSON, auth IDs, paths, and auth indexes are
// never retained after resolving an identity.
type authRuntimeMetadata struct {
	Provider    string
	Type        string
	Email       string
	AccountType string
	Account     string
	Label       string
	BaseURL     string
}

type authRuntimeLookup func(authIndex string) (authRuntimeMetadata, error)

type authIdentityResolver struct {
	lookup authRuntimeLookup
	now    func() time.Time

	mu       sync.Mutex
	entries  map[[sha256.Size]byte]authIdentityCacheEntry
	inFlight map[[sha256.Size]byte]*authIdentityFlight
}

type authIdentityCacheEntry struct {
	metadata authRuntimeMetadata
	err      error
	expires  time.Time
}

type authIdentityFlight struct {
	done chan struct{}
}

type usageIdentity struct {
	Provider string
	Account  string
	BaseURL  string
}

func newAuthIdentityResolver(lookup authRuntimeLookup) *authIdentityResolver {
	return &authIdentityResolver{
		lookup:   lookup,
		now:      time.Now,
		entries:  make(map[[sha256.Size]byte]authIdentityCacheEntry),
		inFlight: make(map[[sha256.Size]byte]*authIdentityFlight),
	}
}

func (r *authIdentityResolver) resolve(authIndex string, usage store.Dimensions) (usageIdentity, error) {
	if r == nil || r.lookup == nil || strings.TrimSpace(authIndex) == "" {
		return usageIdentity{}, nil
	}
	authIndex = strings.TrimSpace(authIndex)
	cacheKey := sha256.Sum256([]byte(authIndex))
	now := r.now()

	r.mu.Lock()
	if cached, ok := r.entries[cacheKey]; ok && now.Before(cached.expires) {
		r.mu.Unlock()
		if cached.err != nil {
			return usageIdentity{}, cached.err
		}
		return identityFromRuntimeMetadata(cached.metadata, usage), nil
	}
	if flight := r.inFlight[cacheKey]; flight != nil {
		r.mu.Unlock()
		<-flight.done
		return r.resolve(authIndex, usage)
	}
	flight := &authIdentityFlight{done: make(chan struct{})}
	r.inFlight[cacheKey] = flight
	r.mu.Unlock()

	metadata, err := r.lookup(authIndex)
	if err == nil {
		metadata = sanitizeAuthRuntimeMetadata(metadata)
	}
	ttl := authIdentitySuccessTTL
	if err != nil {
		ttl = authIdentityFailureTTL
	}

	r.mu.Lock()
	r.entries[cacheKey] = authIdentityCacheEntry{metadata: metadata, err: err, expires: r.now().Add(ttl)}
	delete(r.inFlight, cacheKey)
	close(flight.done)
	r.mu.Unlock()
	if err != nil {
		return usageIdentity{}, err
	}
	return identityFromRuntimeMetadata(metadata, usage), nil
}

func sanitizeAuthRuntimeMetadata(metadata authRuntimeMetadata) authRuntimeMetadata {
	metadata.Provider = store.NormalizeDimension(metadata.Provider)
	metadata.Type = store.NormalizeDimension(metadata.Type)
	metadata.Email = store.SafeAuthAccount(metadata.Email)
	metadata.AccountType = store.NormalizeDimension(metadata.AccountType)
	if strings.EqualFold(metadata.AccountType, "oauth") {
		metadata.Account = store.SafeAuthAccount(metadata.Account)
	} else {
		metadata.Account = ""
	}
	metadata.Label = safeAuthLabel(metadata.Label)
	metadata.BaseURL = store.SanitizeServiceURL(metadata.BaseURL)
	return metadata
}

func identityFromRuntimeMetadata(metadata authRuntimeMetadata, usage store.Dimensions) usageIdentity {
	provider := store.DisplayAuthProvider(firstNonEmptyIdentity(metadata.Provider, metadata.Type, usage.Provider, usage.ExecutorType))
	account := store.SafeAuthAccount(metadata.Email)
	if account == "" && strings.EqualFold(strings.TrimSpace(metadata.AccountType), "oauth") {
		account = store.SafeAuthAccount(metadata.Account)
	}
	if account == "" {
		account = safeAuthLabel(metadata.Label)
	}
	if account == "" {
		account = store.SafeAuthAccount(store.SanitizeDimensionsSource(usage).Source)
	}
	return usageIdentity{Provider: provider, Account: account, BaseURL: metadata.BaseURL}
}
func safeAuthLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || store.LooksLikeCredential(value) {
		return ""
	}
	return store.NormalizeDimension(value)
}

func firstNonEmptyIdentity(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r *pluginRuntime) setAuthRuntimeLookup(lookup authRuntimeLookup) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if lookup == nil {
		r.authResolver = nil
		return
	}
	r.authResolver = newAuthIdentityResolver(lookup)
}

func (r *pluginRuntime) resolveUsageIdentity(usage *store.Usage) {
	if usage == nil || usage.AuthIndex == "" {
		return
	}
	defer func() { usage.AuthIndex = "" }()
	r.mu.RLock()
	resolver := r.authResolver
	r.mu.RUnlock()
	identity, err := resolver.resolve(usage.AuthIndex, usage.Dimensions)
	if err != nil {
		return
	}
	// API-key credentials keep the bare sanitized base_url as Source. Composite
	// Provider-Account labels would be stripped again on every read because
	// safeUsageSource re-runs on persisted dimensions and rewrites non-URL
	// API-key sources to the hardcoded provider service address.
	if store.IsAPIKeyAuth(usage.Dimensions.AuthType) {
		if baseURL := firstNonEmptyIdentity(usage.BaseURL, identity.BaseURL); baseURL != "" {
			usage.Dimensions.Source = store.NormalizeDimension(baseURL)
			return
		}
	}
	usage.Dimensions.Source = store.CanonicalUsageSourceWithIdentity(usage.Dimensions, identity.Provider, identity.Account)
}
