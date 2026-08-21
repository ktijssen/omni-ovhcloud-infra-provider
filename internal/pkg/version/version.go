// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package version contains build-time metadata about the provider binary.
package version

import _ "embed"

var (
	// Tag is the git tag (or description) the binary was built from.
	//go:embed data/tag
	Tag string
	// SHA is the git commit SHA the binary was built from.
	//go:embed data/sha
	SHA string
)
