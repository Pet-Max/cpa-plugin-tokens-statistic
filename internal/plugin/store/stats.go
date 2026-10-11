package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Pet-Max/cpa-plugin-tokens-statistic/internal/plugin/errs"
	"time"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) Query(rangeName string) (StatsResponse, error) {
	queryRange, err := presetUsageRange(rangeName, time.Now().UTC())
	if err != nil {
		return StatsResponse{}, err
	}
	return s.queryStats(queryRange)
}

func (s *Store) queryStats(queryRange Range) (StatsResponse, error) {
	return s.QueryStatsByFilter(queryRange, Filter{})
}

func (s *Store) queryStatsBySource(queryRange Range, source string) (StatsResponse, error) {
	return s.QueryStatsByFilter(queryRange, newUsageFilter(source, ""))
}

func (s *Store) QueryStatsByFilter(queryRange Range, filter Filter) (StatsResponse, error) {
	resp := make(chan queryResult, 1)
	if err := s.send(queryCommand{queryRange: queryRange, filter: filter, resp: resp}); err != nil {
		return StatsResponse{}, err
	}
	result := <-resp
	return result.stats, result.err
}

func (s *Store) QueryInitialStatsByFilter(queryRange Range, filter Filter) (InitialStatsResponse, error) {
	resp := make(chan queryResult, 1)
	if err := s.send(queryCommand{queryRange: queryRange, filter: filter, mode: statsQueryInitial, resp: resp}); err != nil {
		return InitialStatsResponse{}, err
	}
	result := <-resp
	return result.initial, result.err
}

func (s *Store) QueryStatsTrendByFilter(queryRange Range, filter Filter) (StatsTrendResponse, error) {
	resp := make(chan queryResult, 1)
	if err := s.send(queryCommand{queryRange: queryRange, filter: filter, mode: statsQueryTrend, resp: resp}); err != nil {
		return StatsTrendResponse{}, err
	}
	result := <-resp
	return result.trend, result.err
}

func (s *Store) QueryGroupsByFilter(queryRange Range, filter Filter) (GroupStatsPage, error) {
	resp := make(chan queryResult, 1)
	if err := s.send(queryCommand{queryRange: queryRange, filter: filter, mode: statsQueryGroups, resp: resp}); err != nil {
		return GroupStatsPage{}, err
	}
	result := <-resp
	return result.groups, result.err
}

func requiresExactStats(queryRange Range) bool {
	return queryRange.Name == "custom" &&
		(queryRange.Start.Second() != 0 || queryRange.Start.Nanosecond() != 0 ||
			queryRange.End.Second() != 0 || queryRange.End.Nanosecond() != 0)
}

func (a *storeActor) queryExactStats(queryRange Range, filter Filter, now time.Time) (StatsResponse, error) {
	if err := queryRange.validate(); err != nil {
		return StatsResponse{}, errs.WithStatus(400, "%v", err)
	}
	data := make(map[aggregateKey]Counters)
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
			if !requestedAt.Before(queryRange.End) {
				continue
			}
			if requestedAt.Before(queryRange.Start) {
				break
			}
			var item RequestDetail
			if err := json.Unmarshal(value, &item); err != nil {
				return fmt.Errorf("decode request detail: %w", err)
			}
			dimensions := SanitizeDimensionsSource(item.Dimensions)
			dimensions.APIKey = ""
			counters := item.Counters
			if item.LatencyNS > 0 {
				counters.TotalLatencyNS = item.LatencyNS
				counters.LatencySamples = 1
			}
			if item.TTFTNS > 0 {
				counters.TotalTTFTNS = item.TTFTNS
				counters.TTFTSamples = 1
			}
			bucket := aggregateKey{Hour: requestedAt.Truncate(time.Minute).Unix(), Dimensions: dimensions}
			combined := data[bucket]
			combined.add(counters)
			data[bucket] = combined
		}
		return nil
	})
	if err != nil {
		return StatsResponse{}, fmt.Errorf("query exact stats: %w", err)
	}
	return buildStatsForRangeWithFilter(data, a.since, a.lastUsed, Range{Name: queryRange.Name}, filter, now, a.apiKeyCiphertexts), nil
}
