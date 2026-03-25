package nmos

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"
)

// RegistryEntry represents a discovered NMOS registry (RDS) on the network.
type RegistryEntry struct {
	Host     string
	Port     int
	Priority int      // from TXT record "pri=N", lower = higher priority
	Versions []string // from TXT record "api_ver=v1.0,v1.1,v1.2,v1.3"
	Protocol string   // from TXT record "api_proto=http" or "https"
}

// TXTInfo holds parsed DNS-SD TXT record fields for an NMOS service.
type TXTInfo struct {
	Priority int
	Versions []string
	Protocol string
	Auth     bool
}

// StopFunc is a function that stops an advertised service.
type StopFunc func()

// ParseTXTRecords parses DNS-SD TXT records into a TXTInfo struct.
// Expected keys:
//   - api_ver=v1.0,v1.1,v1.2,v1.3  (comma-separated versions)
//   - api_proto=http                (protocol string)
//   - api_auth=false                (boolean)
//   - pri=100                       (priority integer, lower = higher priority)
//
// Missing pri defaults to 100, missing api_proto defaults to "http".
func ParseTXTRecords(records []string) TXTInfo {
	info := TXTInfo{
		Priority: 100,
		Protocol: "http",
	}

	for _, rec := range records {
		k, v, ok := strings.Cut(rec, "=")
		if !ok {
			continue
		}
		switch k {
		case "api_ver":
			info.Versions = strings.Split(v, ",")
		case "api_proto":
			info.Protocol = v
		case "api_auth":
			info.Auth = v == "true"
		case "pri":
			p, err := strconv.Atoi(v)
			if err == nil {
				info.Priority = p
			}
		}
	}

	return info
}

// SortByPriority sorts registry entries ascending by Priority
// (lower value = higher priority).
func SortByPriority(entries []RegistryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Priority < entries[j].Priority
	})
}

// FilterByVersion returns only entries whose Versions slice contains
// the specified version string.
func FilterByVersion(entries []RegistryEntry, version string) []RegistryEntry {
	var result []RegistryEntry
	for _, e := range entries {
		for _, v := range e.Versions {
			if v == version {
				result = append(result, e)
				break
			}
		}
	}
	return result
}

// BrowseRegistries discovers NMOS registries on the local network using
// DNS-SD browsing for the _nmos-register._tcp service type.
// It collects entries for up to 3 seconds (or until context is done),
// parses TXT records, and returns results sorted by priority.
func BrowseRegistries(ctx context.Context) ([]RegistryEntry, error) {
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("nmos: create resolver: %w", err)
	}

	entryCh := make(chan *zeroconf.ServiceEntry)
	var entries []RegistryEntry

	// Collect in background.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entryCh {
			info := ParseTXTRecords(e.Text)
			host := e.HostName
			if len(e.AddrIPv4) > 0 {
				host = e.AddrIPv4[0].String()
			} else if len(e.AddrIPv6) > 0 {
				host = e.AddrIPv6[0].String()
			}
			entries = append(entries, RegistryEntry{
				Host:     host,
				Port:     e.Port,
				Priority: info.Priority,
				Versions: info.Versions,
				Protocol: info.Protocol,
			})
		}
	}()

	// Browse with a 3-second timeout.
	browseCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := resolver.Browse(browseCtx, "_nmos-register._tcp", "local.", entryCh); err != nil {
		return nil, fmt.Errorf("nmos: browse registries: %w", err)
	}

	// Wait for browsing to complete.
	<-browseCtx.Done()
	<-done

	SortByPriority(entries)
	return entries, nil
}

// AdvertiseNode advertises this NMOS node on the local network using
// DNS-SD for the _nmos-node._tcp service type. It returns a StopFunc
// that should be called to un-advertise when shutting down.
func AdvertiseNode(ctx context.Context, instanceName string, port int, versions []string) (StopFunc, error) {
	txt := []string{
		"api_ver=" + strings.Join(versions, ","),
		"api_proto=http",
		"api_auth=false",
		"pri=100",
	}

	server, err := zeroconf.Register(
		instanceName,
		"_nmos-node._tcp",
		"local.",
		port,
		txt,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("nmos: advertise node: %w", err)
	}

	// Shut down the server when context is cancelled.
	go func() {
		<-ctx.Done()
		server.Shutdown()
	}()

	return func() {
		server.Shutdown()
	}, nil
}
