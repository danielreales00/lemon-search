package ingest

import (
	"regexp"
	"strings"
)

// GeoDecision is the outcome of the Miami geo filter for one record. The two
// drop reasons are distinct because the ingest end-of-run report counts
// "dropped (geo)" and "dropped (no addr)" separately.
type GeoDecision int

const (
	// Keep means the record is in Miami (valid bbox coords or an FL address).
	Keep GeoDecision = iota
	// DropNonMiami means the record has an address but is outside Miami.
	DropNonMiami
	// DropNoAddress means the record has neither coordinates nor an address.
	DropNoAddress
)

// Miami-Dade-ish bounding box (see docs/data/quality.md). A record whose
// coordinates fall inside this box is treated as in-Miami.
const (
	minLat = 25.10
	maxLat = 26.10
	minLng = -80.95
	maxLng = -80.05
)

// flAddress matches a US-Florida address suffix ("…, FL" / "…, Florida"),
// case-insensitively. It rescues real Miami businesses whose coordinates are
// null or slightly off the bbox but whose address still names Florida.
var flAddress = regexp.MustCompile(`(?i),\s*FL\b|,\s*Florida\b`)

// GeoFilter decides whether a record is in Miami. Coordinates are nullable
// (nil lat/lng means the source row had no usable geo). A record is kept when
// its coordinates are inside the bbox OR its address names Florida; an FL
// address overrides missing or out-of-box coordinates because the addresses
// are more reliable than the partially-null lat/lng (97% coverage).
//
// When neither holds, the drop reason distinguishes a no-address row (nothing
// to geolocate on) from a located-but-non-Miami row.
func GeoFilter(lat, lng *float64, address string) GeoDecision {
	if inMiamiBBox(lat, lng) || flAddress.MatchString(address) {
		return Keep
	}
	if lat == nil && lng == nil && strings.TrimSpace(address) == "" {
		return DropNoAddress
	}
	return DropNonMiami
}

func inMiamiBBox(lat, lng *float64) bool {
	if lat == nil || lng == nil {
		return false
	}
	return *lat >= minLat && *lat <= maxLat && *lng >= minLng && *lng <= maxLng
}
