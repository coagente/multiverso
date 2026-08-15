package oracle

import "testing"

func TestParseCoverageBP(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    int64
		wantErr bool
	}{
		{
			// 12 of 14 statements = 85.714…% → 8571 bp. The report also
			// carries percent_covered as a float; it is never parsed.
			name: "recorded coverage.py report",
			json: string(fixtureBytes("coverage.json")),
			want: 8571,
		},
		{
			name: "half rounds up",
			json: `{"totals":{"covered_lines":2,"num_statements":3}}`,
			want: 6667,
		},
		{
			name: "below half rounds down",
			json: `{"totals":{"covered_lines":1,"num_statements":3}}`,
			want: 3333,
		},
		{
			name: "full coverage",
			json: `{"totals":{"covered_lines":41,"num_statements":41}}`,
			want: 10000,
		},
		{
			name: "no coverage",
			json: `{"totals":{"covered_lines":0,"num_statements":7}}`,
			want: 0,
		},
		{
			// "Nothing to cover" is not evidence of coverage: a fabricated
			// 10000 would silently satisfy coverage-at-least.
			name:    "no statements",
			json:    `{"totals":{"covered_lines":0,"num_statements":0}}`,
			wantErr: true,
		},
		{
			name:    "totals absent",
			json:    `{"meta":{"version":"7.15.4"},"files":{}}`,
			wantErr: true,
		},
		{
			name:    "covered exceeds statements",
			json:    `{"totals":{"covered_lines":9,"num_statements":7}}`,
			wantErr: true,
		},
		{
			name:    "negative covered",
			json:    `{"totals":{"covered_lines":-1,"num_statements":7}}`,
			wantErr: true,
		},
		{
			name:    "fractional counter",
			json:    `{"totals":{"covered_lines":12.5,"num_statements":14}}`,
			wantErr: true,
		},
		{
			name:    "not json",
			json:    "Traceback (most recent call last):\n",
			wantErr: true,
		},
		{
			name:    "truncated json",
			json:    `{"totals":{"covered_lines":12,`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCoverageBP([]byte(tt.json))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCoverageBP: want error, got %d", got)
				}
				if got != 0 {
					t.Errorf("parseCoverageBP returned %d alongside an error; want 0", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCoverageBP: %v", err)
			}
			if got != tt.want {
				t.Errorf("coverage_bp = %d, want %d", got, tt.want)
			}
		})
	}
}

// fixtureBytes is the non-*testing.T reader used inside table literals.
func fixtureBytes(name string) []byte {
	b, err := readFixture(name)
	if err != nil {
		panic(err)
	}
	return b
}
