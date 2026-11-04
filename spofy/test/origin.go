package main

import (
	"crypto/rand"
	"net/http"
	"strconv"
)

var blob = func() []byte { b := make([]byte, 64<<20); rand.Read(b); return b }()

func main() {
	http.HandleFunc("/f", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("mb"))
		if n <= 0 { n = 64 }
		w.Header().Set("Content-Length", strconv.Itoa(n<<20))
		for i := 0; i < n; i += 64 {
			m := 64
			if n-i < 64 { m = n - i }
			if _, err := w.Write(blob[:m<<20]); err != nil { return }
		}
	})
	http.HandleFunc("/s", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	http.ListenAndServe("127.0.0.1:18080", nil)
}
