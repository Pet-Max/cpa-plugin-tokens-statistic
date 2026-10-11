package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) QueryRequests(rangeName string, offset, limit int, model string) (RequestPage, error) {
	queryRange, err := presetUsageRange(rangeName, time.Now().UTC())
	if err != nil {
		return RequestPage{}, err
	}
	return s.queryRequestPage(queryRange, offset, limit, model)
}

func (s *Store) queryRequestPage(queryRange Range, offset, limit int, model string) (RequestPage, error) {
	return s.QueryRequestPageByFilter(queryRange, offset, limit, model, Filter{}, "")
}

func (s *Store) queryRequestPageBySource(queryRange Range, offset, limit int, model, source, resultFilter string) (RequestPage, error) {
	return s.QueryRequestPageByFilter(queryRange, offset, limit, model, newUsageFilter(source, ""), resultFilter)
}

func (s *Store) QueryRequestPageByFilter(queryRange Range, offset, limit int, model string, filter Filter, resultFilter string) (RequestPage, error) {
	resp := make(chan requestQueryResult, 1)
	if err := s.send(requestQueryCommand{queryRange: queryRange, offset: offset, limit: limit, model: model, filter: filter, result: resultFilter, resp: resp}); err != nil {
		return RequestPage{}, err
	}
	result := <-resp
	return result.page, result.err
}

func (a *storeActor) queryRequests(queryRange Range, offset, limit int, model string, filter Filter, resultFilter string, now time.Time) (RequestPage, error) {
	if err := queryRange.validate(); err != nil {
		return RequestPage{}, errs.WithStatus(400, "%v", err)
	}
	if offset < 0 {
		return RequestPage{}, errs.WithStatus(400, "offset must not be negative")
	}
	if limit == 0 {
		limit = DefaultRequestPageSize
	}
	if limit < 1 || limit > maxRequestPageSize {
		return RequestPage{}, errs.WithStatus(400, "limit must be between 1 and %d", maxRequestPageSize)
	}
	if resultFilter != "" && resultFilter != "success" && resultFilter != "failed" {
		return RequestPage{}, errs.WithStatus(400, "result must be success or failed")
	}

	resolver := newModelPriceResolver(a.modelPrices, a.priceSyncSettings)
	page := RequestPage{
		GeneratedAt:       now.UTC(),
		Range:             queryRange.Name,
		PriceBookRevision: a.priceRevision,
		Offset:            offset,
		Limit:             limit,
		Items:             make([]RequestDetail, 0, limit),
	}
	err := a.db.View(func(tx *bolt.Tx) error {
		requests := tx.Bucket(requestsBucket)
		if requests == nil {
			return errors.New("requests bucket is missing")
		}
		cursor := requests.Cursor()
		for key, value := cursor.Last(); key != nil; key, value = cursor.Prev() {
			if len(key) != 16 || value == nil {
				continue
			}
			requestedAt := time.Unix(0, decodeInt64(key[:8])).UTC()
			if !queryRange.End.IsZero() && !requestedAt.Before(queryRange.End) {
				continue
			}
			if !queryRange.Start.IsZero() && requestedAt.Before(queryRange.Start) {
				break
			}
			var item RequestDetail
			if err := json.Unmarshal(value, &item); err != nil {
				return fmt.Errorf("decode request detail: %w", err)
			}
			item.Dimensions = SanitizeDimensionsSource(item.Dimensions)
			// Stored records do not retain whether TotalTokens was explicit. Use
			// the conservative unknown-provider path here; known protocol
			// semantics still correct historical separate-reasoning records.
			item.TPS = requestTPS(item, false)
			if model != "" && !ModelFilterMatches(model, item.Model) {
				continue
			}
			if !filter.matches(item.Dimensions) {
				continue
			}
			if (resultFilter == "success" && item.Failed) || (resultFilter == "failed" && !item.Failed) {
				continue
			}
			page.Total++
			if page.Total <= offset || len(page.Items) >= limit {
				continue
			}
			cost := estimateRequestCostWithResolver(item, resolver)
			item.EstimatedCost = &cost
			page.Items = append(page.Items, item)
		}
		return nil
	})
	if err != nil {
		return RequestPage{}, fmt.Errorf("query request details: %w", err)
	}
	return page, nil
}

func encodeRequestKey(unixNano int64, sequence uint64) []byte {
	result := make([]byte, 16)
	copy(result[:8], encodeInt64(unixNano))
	binary.BigEndian.PutUint64(result[8:], sequence)
	return result
}

func pruneRequestsBucket(requests *bolt.Bucket, cutoffUnixNano int64) error {
	cursor := requests.Cursor()
	for key, _ := cursor.First(); key != nil; key, _ = cursor.Next() {
		if len(key) != 16 {
			continue
		}
		if decodeInt64(key[:8]) >= cutoffUnixNano {
			break
		}
		if err := cursor.Delete(); err != nil {
			return err
		}
	}
	return nil
}
