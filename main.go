package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"

	log "gopkg.in/Sirupsen/logrus.v0"

	"./typestat"
)

// config
var addr = flag.String("addr", ":8080", "Address to listen on")
var mapURL = flag.String("map", "http://localhost:3000", "MAP Rails server address")

// package globals
var mapProxy *httputil.ReverseProxy
var static = map[string]http.HandlerFunc{}
var public = http.Dir("../map/public")
var publicHandler http.Handler
var uploadsCMSRe *regexp.Regexp

func init() {
	ok, err := typestat.IsDir(string(public))
	if err != nil {
		panic(err)
	}
	if !ok {
		panic(fmt.Sprintf("public = %q - not a directory", public))
	}

	uploadsCMSRe = regexp.MustCompile(`^/uploads_cms/(\w+)-image-(\d{1,4})(\d{1,4})?/(.+)$`)

	static["/favicon.ico"] = http.NotFound
	static["/mini-profiler-resources/results"] = http.NotFound
	publicHandler = http.FileServer(public)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	flag.Parse()
	url, err := url.Parse(*mapURL)
	if err != nil {
		log.Fatal(err)
	}
	mapProxy = httputil.NewSingleHostReverseProxy(url)

	log.SetLevel(log.DebugLevel)
	log.Infof("Proxying MAP requests to %s", url)
	log.Infof("Listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(route)))
}

func route(w http.ResponseWriter, r *http.Request) {
	rlog := log.WithFields(log.Fields{"path": r.URL.Path})
	if h := static[r.URL.Path]; h != nil {
		rlog.Infof("static path match")
		h(w, r)
		return
	}
	if m := uploadsCMSRe.FindStringSubmatch(r.URL.Path); m != nil {
		rlog.Infof("sending uploads_cms")
		r.URL.Path = fmt.Sprintf("/uploads/cms/%s/image/%s/%s/%s", m[1], m[2], m[3], m[4])
		publicHandler.ServeHTTP(w, r)
		return
	}
	ok, err := typestat.IsFile(filepath.Join(string(public), r.URL.Path))
	if err != nil {
		rlog.Errorf("while testing file: %v", err)
		w.WriteHeader(500)
		return
	}
	if ok {
		rlog.Infof("sending file")
		publicHandler.ServeHTTP(w, r)
		return
	}
	rlog.Infof("proxying to %s", *mapURL)
	mapProxy.ServeHTTP(w, r)
}
