// Copyright (c) 2026 Probo Inc <hello@probo.com>.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package probo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersionFromFileName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fileName  string
		wantMajor int
		wantMinor int
		wantOk    bool
	}{
		{
			name:      "project policy with v prefix",
			fileName:  "Project - Beleidsnaam - v1.0",
			wantMajor: 1,
			wantMinor: 0,
			wantOk:    true,
		},
		{
			name:      "project policy with v1.1",
			fileName:  "Project - Beleidsnaam - v1.1",
			wantMajor: 1,
			wantMinor: 1,
			wantOk:    true,
		},
		{
			name:      "project policy with v2.3 and docx extension",
			fileName:  "Project - Beleidsnaam - v2.3.docx",
			wantMajor: 2,
			wantMinor: 3,
			wantOk:    true,
		},
		{
			name:      "dutch policy name with uppercase V",
			fileName:  "Informatiebeveiligingsbeleid V1.0",
			wantMajor: 1,
			wantMinor: 0,
			wantOk:    true,
		},
		{
			name:      "standalone v1.0",
			fileName:  "v1.0",
			wantMajor: 1,
			wantMinor: 0,
			wantOk:    true,
		},
		{
			name:      "policy with number only 2.1",
			fileName:  "Policy - 2.1",
			wantMajor: 2,
			wantMinor: 1,
			wantOk:    true,
		},
		{
			name:      "project with parentheses like user example",
			fileName:  "(Project) - (Beleidsnaam) - v1.4",
			wantMajor: 1,
			wantMinor: 4,
			wantOk:    true,
		},
		{
			name:      "v0.1 with parentheses",
			fileName:  "(Acme) - (Informatiebeveiliging) - v0.1",
			wantMajor: 0,
			wantMinor: 1,
			wantOk:    true,
		},
		{
			name:      "version with suffix in parentheses",
			fileName:  "Beleidsplan v3.0 (definitief)",
			wantMajor: 3,
			wantMinor: 0,
			wantOk:    true,
		},
		{
			name:      "double digit version",
			fileName:  "Policy v10.2",
			wantMajor: 10,
			wantMinor: 2,
			wantOk:    true,
		},
		{
			name:      "year 2026 without dot",
			fileName:  "Security Policy 2026",
			wantMajor: 0,
			wantMinor: 0,
			wantOk:    false,
		},
		{
			name:      "no version in name",
			fileName:  "General Company Security Policy",
			wantMajor: 0,
			wantMinor: 0,
			wantOk:    false,
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.name,
			func(t *testing.T) {
				t.Parallel()

				major, minor, ok := parseVersionFromFileName(tt.fileName)
				require.Equal(t, tt.wantOk, ok)
				require.Equal(t, tt.wantMajor, major)
				require.Equal(t, tt.wantMinor, minor)
			},
		)
	}
}
