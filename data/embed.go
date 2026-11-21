// Package data embeds the band catalog so sdrd works from any directory.
package data

import _ "embed"

// Bands is bands.json, the single source of truth for the band catalog.
//
//go:embed bands.json
var Bands []byte
