package ingest

import "testing"

func ptr(f float64) *float64 { return &f }

func TestGeoFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		lat     *float64
		lng     *float64
		address string
		want    GeoDecision
	}{
		{
			name:    "inside bbox keeps",
			lat:     ptr(25.7617),
			lng:     ptr(-80.1918),
			address: "100 Biscayne Blvd, Miami",
			want:    Keep,
		},
		{
			name:    "inside bbox keeps even without address",
			lat:     ptr(25.80),
			lng:     ptr(-80.20),
			address: "",
			want:    Keep,
		},
		{
			name:    "inside bbox keeps with blank address",
			lat:     ptr(25.80),
			lng:     ptr(-80.20),
			address: "   ",
			want:    Keep,
		},
		{
			name:    "outside bbox non-fl address drops as non-miami",
			lat:     ptr(40.7128),
			lng:     ptr(-74.0060),
			address: "350 5th Ave, New York, NY",
			want:    DropNonMiami,
		},
		{
			name:    "null coords non-fl address drops as non-miami",
			lat:     nil,
			lng:     nil,
			address: "350 5th Ave, New York, NY",
			want:    DropNonMiami,
		},
		{
			name:    "null coords fl address keeps",
			lat:     nil,
			lng:     nil,
			address: "123 Ocean Dr, Miami Beach, FL 33139",
			want:    Keep,
		},
		{
			name:    "null coords no address drops as no-address",
			lat:     nil,
			lng:     nil,
			address: "",
			want:    DropNoAddress,
		},
		{
			name:    "null coords blank address drops as no-address",
			lat:     nil,
			lng:     nil,
			address: "  \t \n ",
			want:    DropNoAddress,
		},
		{
			name:    "versailles france sample drops",
			lat:     ptr(48.8049),
			lng:     ptr(2.1204),
			address: "Place d'Armes, 78000 Versailles, France",
			want:    DropNonMiami,
		},
		{
			name:    "fl address overrides out-of-bbox coords",
			lat:     ptr(48.8049),
			lng:     ptr(2.1204),
			address: "123 Ocean Dr, Miami, FL",
			want:    Keep,
		},
		{
			name:    "lowercase fl is case-insensitive",
			lat:     nil,
			lng:     nil,
			address: "123 Main St, miami, fl 33101",
			want:    Keep,
		},
		{
			name:    "florida full word keeps",
			lat:     nil,
			lng:     nil,
			address: "456 Palm Ave, Orlando, Florida",
			want:    Keep,
		},
		{
			name:    "lowercase florida is case-insensitive",
			lat:     nil,
			lng:     nil,
			address: "456 Palm Ave, tampa, florida",
			want:    Keep,
		},
		{
			name:    "only one of lat lng present and outside drops",
			lat:     ptr(25.80),
			lng:     nil,
			address: "Somewhere, NY",
			want:    DropNonMiami,
		},
		{
			name:    "flx without word boundary does not match",
			lat:     nil,
			lng:     nil,
			address: "1 Reflexology Way, Reno, FLX",
			want:    DropNonMiami,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := GeoFilter(tc.lat, tc.lng, tc.address)
			if got != tc.want {
				t.Errorf("GeoFilter() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGeoFilterBBoxEdges(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		lat  float64
		lng  float64
		want GeoDecision
	}{
		{"min corner inclusive", minLat, minLng, Keep},
		{"max corner inclusive", maxLat, maxLng, Keep},
		{"just below min lat", minLat - 0.0001, minLng, DropNonMiami},
		{"just above max lat", maxLat + 0.0001, maxLng, DropNonMiami},
		{"just west of min lng", minLat, minLng - 0.0001, DropNonMiami},
		{"just east of max lng", minLat, maxLng + 0.0001, DropNonMiami},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Non-FL address so only the bbox decides Keep vs DropNonMiami.
			got := GeoFilter(ptr(tc.lat), ptr(tc.lng), "Somewhere, NY")
			if got != tc.want {
				t.Errorf("GeoFilter(%v,%v) = %v, want %v", tc.lat, tc.lng, got, tc.want)
			}
		})
	}
}
