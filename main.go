package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"

	"./typestat"
)

import log "gopkg.in/Sirupsen/logrus.v0"

// config
var addr = flag.String("addr", ":8080", "Address to listen on")
var mapArg = flag.String("map", ":3000", "MAP Rails server address")

// package globals
var mapURL string
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
	url, err := buildMAPURL(*mapArg)
	if err != nil {
		log.Fatal(err)
	}
	mapURL = url

	log.SetLevel(log.DebugLevel)
	log.Infof("Proxying MAP requests to %s", mapURL)
	log.Infof("Listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(route)))
}

func buildMAPURL(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "3000"
	}
	return "http://" + host + ":" + port, nil
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
	proxy(w, r)
}

func handleCmsImage(w http.ResponseWriter, r *http.Request) {
	log.Infof("new request: CmsImage %s", r.URL.Path)
	w.WriteHeader(404)
}

func proxy(w http.ResponseWriter, r *http.Request) {
	log.Infof("new request: %s => %s", r.URL.Path, mapURL)
	r2, err := http.NewRequest(r.Method, mapURL+r.URL.Path, r.Body)
	if err != nil {
		w.WriteHeader(502)
		return
	}
	copyHeader(r2.Header, r.Header)
	r2.Host = r.Host
	client := http.Client{}
	resp, err := client.Do(r2)
	if err != nil {
		// TODO respond with 503
		w.WriteHeader(502)
		return
	}
	defer resp.Body.Close()
	copyHeader(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func copyHeader(dst, src http.Header) {
	for key := range src {
		dst.Set(key, src.Get(key))
	}
}
