// Package dnstest provides an in-memory DNS provider for tests.
package dnstest

import (
	"context"
	"fmt"
	"sync"

	"github.com/libdns/libdns"
)

// Memory holds the records of a fixed set of zones. Zones it does not hold return an error, as a real
// provider does for a zone the credentials cannot reach.
type Memory struct {
	mu      sync.Mutex
	records map[string]map[string]libdns.RR // zone → "name type" → record
}

// New returns a provider holding the given zones, e.g. "example.com.".
func New(zones ...string) *Memory {
	m := &Memory{records: map[string]map[string]libdns.RR{}}
	for _, z := range zones {
		m.records[z] = map[string]libdns.RR{}
	}
	return m
}

func (m *Memory) zone(zone string) (map[string]libdns.RR, error) {
	z, ok := m.records[zone]
	if !ok {
		return nil, fmt.Errorf("zone %s not found", zone)
	}
	return z, nil
}

func (m *Memory) GetRecords(_ context.Context, zone string) ([]libdns.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, err := m.zone(zone)
	if err != nil {
		return nil, err
	}
	var out []libdns.Record
	for _, rr := range z {
		out = append(out, rr)
	}
	return out, nil
}

func (m *Memory) SetRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, err := m.zone(zone)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		rr := r.RR()
		z[rr.Name+" "+rr.Type] = rr
	}
	return recs, nil
}

func (m *Memory) DeleteRecords(_ context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, err := m.zone(zone)
	if err != nil {
		return nil, err
	}
	for _, r := range recs {
		rr := r.RR()
		delete(z, rr.Name+" "+rr.Type)
	}
	return recs, nil
}

// Lookup returns the data of the record name/type in zone, or "".
func (m *Memory) Lookup(zone, name, typ string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[zone][name+" "+typ].Data
}
