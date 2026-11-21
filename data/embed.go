// Package data embeds the band catalog so sdrd works from any directory.
package data

import _ "embed"

// Bands is bands.json, the single source of truth for the band catalog.
//
//go:embed bands.json
var Bands []byte

// ICAOCountries is icao_countries.json: ICAO 24-bit (Mode S) address blocks per
// country, from the ICAO Annex 10 Vol III allocations.
//
//go:embed icao_countries.json
var ICAOCountries []byte
