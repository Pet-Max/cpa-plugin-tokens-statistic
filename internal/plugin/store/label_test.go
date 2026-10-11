package store

import (
	"strings"
	"testing"
	"time"
)

func TestAPIKeyLabelCallsReturnAfterStoreClose(t *testing.T) {
	config := testConfig(t)
	store, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() {
		_, err := store.APIKeyLabels()
		done <- err
	}()
	go func() { done <- store.SetAPIKeyLabel(strings.Repeat("0", 32), "") }()
	for range 2 {
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("closed store label call succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("closed store label call blocked")
		}
	}
}
