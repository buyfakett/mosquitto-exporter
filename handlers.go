package main

import (
	"net/http"
)

/*
 * Root and Healthcheck
 */

var landingPage = []byte(`<html>
<head><title>Mosquitto exporter</title></head>
<body>
<h1>Mosquitto exporter</h1>
<p><a href='/metrics'>Metrics</a></p>
</body>
</html>
`)

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(landingPage)
}
