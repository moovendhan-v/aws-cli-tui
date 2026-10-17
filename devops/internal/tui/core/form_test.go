package core

import "testing"

func TestRankOptionsPrefersLabelWords(t *testing.T) {
	opts := []Option{
		{Value: "i-0a1b2c3d4e5f60003", Label: "worker-batch", Detail: "i-0a1b2c3d4e5f60003 · stopped · 10.0.2.40"},
		{Value: "i-0a1b2c3d4e5f60001", Label: "web-01", Detail: "i-0a1b2c3d4e5f60001 · running · 10.0.1.12"},
		{Value: "i-0a1b2c3d4e5f60002", Label: "web-02", Detail: "i-0a1b2c3d4e5f60002 · running · 10.0.1.13"},
	}
	cases := map[string]string{"web 02": "web-02", "web-01": "web-01", "60003": "worker-batch", "10.0.1.13": "web-02", "wrkbtch": "worker-batch"}
	for q, want := range cases {
		got := RankOptions(opts, q)
		if len(got) == 0 || opts[got[0]].Label != want {
			t.Errorf("%q → %v, want %s first", q, got, want)
		}
	}
}
