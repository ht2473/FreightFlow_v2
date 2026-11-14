package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestWithClientForwardedFor(t *testing.T) {
	t.Parallel()
	proxies, err := ParsePrefixes([]string{"10.0.0.0/8", " 192.0.2.1 ", ""})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"без прокси", "203.0.113.5:4000", nil, "203.0.113.5"},
		{"недоверенный источник", "203.0.113.5:4000", []string{"198.51.100.7"}, "203.0.113.5"},
		{"доверенный прокси", "10.1.2.3:4000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"подделка слева", "10.1.2.3:4000", []string{"1.1.1.1, 198.51.100.7"}, "198.51.100.7"},
		{"цепочка прокси", "192.0.2.1:4000", []string{"198.51.100.7", "10.9.9.9"}, "198.51.100.7"},
		{"мусор в заголовке", "10.1.2.3:4000", []string{"не адрес"}, "10.1.2.3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got netip.Addr
			h := WithClient(proxies...)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = ClientFrom(r.Context()).IP
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.String() != tc.want {
				t.Fatalf("client %s, want %s", got, tc.want)
			}
		})
	}
}

func TestParsePrefixesRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := ParsePrefixes([]string{"10.0.0.0/33"}); err == nil {
		t.Fatal("want error")
	}
}
