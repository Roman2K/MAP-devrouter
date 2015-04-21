package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"

	log "gopkg.in/Sirupsen/logrus.v0"

	"./filetest"
)

// config
var addr = flag.String("addr", ":8080", "Address to listen on")
var mapURL = flag.String("map", "http://localhost:3000", "MAP (ecommerce) server URL")
var editorURL = flag.String("editor", "http://localhost:3001", "Editor server URL")

// package globals
var mapProxy *httputil.ReverseProxy
var editorProxy *httputil.ReverseProxy
var static = map[string]http.HandlerFunc{}
var public = http.Dir("../map/public")
var publicHandler http.Handler
var uploadsCMSRe *regexp.Regexp

func init() {
	ok, err := filetest.IsDir(string(public))
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
	setProxy(&mapProxy, *mapURL)
	setProxy(&editorProxy, *editorURL)

	log.SetLevel(log.DebugLevel)
	log.Infof("proxying MAP requests to %s", *mapURL)
	log.Infof("proxying Editor requests to %s", *editorURL)
	log.Infof("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(route)))
}

func setProxy(ptr **httputil.ReverseProxy, rawurl string) {
	url, err := url.Parse(rawurl)
	if err != nil {
		log.Fatal(err)
	}
	*ptr = httputil.NewSingleHostReverseProxy(url)
}

func route(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		log.Error("failed to extract domain from Host: %s", r.Host)
		w.WriteHeader(500)
		return
	}
	rlog := log.WithFields(log.Fields{"Host": host, "path": r.URL.Path})
	if host == "app.map.dev" {
		rlog.Info("proxying to Editor")
		editorProxy.ServeHTTP(w, r)
		return
	}
	if h := static[r.URL.Path]; h != nil {
		rlog.Info("static path match")
		h(w, r)
		return
	}
	if m := uploadsCMSRe.FindStringSubmatch(r.URL.Path); m != nil {
		rlog.Info("sending uploads_cms")
		r.URL.Path = fmt.Sprintf("/uploads/cms/%s/image/%s/%s/%s", m[1], m[2], m[3], m[4])
		publicHandler.ServeHTTP(w, r)
		return
	}
	ok, err := filetest.IsFile(filepath.Join(string(public), r.URL.Path))
	if err != nil {
		rlog.Errorf("while testing file: %v", err)
		w.WriteHeader(500)
		return
	}
	if ok {
		rlog.Info("sending file")
		publicHandler.ServeHTTP(w, r)
		return
	}
	rlog.Info("proxying to MAP")
	mapProxy.ServeHTTP(w, r)
}
