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
	"strings"

	log "gopkg.in/Sirupsen/logrus.v0"

	"./filetest"
)

// config
var (
	addr       = flag.String("addr", ":8080", "Address to listen on")
	mapURL     = flag.String("map", "http://localhost:3000", "MAP (ecommerce) server URL")
	editorURL  = flag.String("editor", "http://localhost:3001", "Editor server URL")
	storageURL = flag.String("storage", "http://localhost:3010", "Storage server URL")
)

// package globals
var (
	mapProxy      *httputil.ReverseProxy
	editorProxy   *httputil.ReverseProxy
	storageProxy  *httputil.ReverseProxy
	static        = map[string]http.HandlerFunc{}
	public        = http.Dir("../map/public")
	publicHandler http.Handler
	uploadsCMSRe  *regexp.Regexp
)

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
	setProxy(&storageProxy, *storageURL)

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

type ResponseWriter struct {
	final   http.ResponseWriter
	req     *http.Request
	log     *log.Entry
	written bool
}

func (w *ResponseWriter) Header() http.Header {
	return w.final.Header()
}

func (w *ResponseWriter) Write(chunk []byte) (int, error) {
	w.written = true
	return w.final.Write(chunk)
}

func (w *ResponseWriter) WriteHeader(status int) {
	if redir := w.final.Header().Get("X-Accel-Redirect"); redir != "" {
		rlog := w.log.WithFields(log.Fields{"X-Accel-Redirect": redir})
		if w.written {
			rlog.Errorf("attempted to X-Accel-Redirect after write")
		} else {
			if path := strings.TrimPrefix(redir, "/storage/"); len(path) < len(redir) {
				rlog.Debugf("proxying to Storage")
				w.req.URL.Path = path
				w.written = true
				w.Header().Del("Content-Length")
				storageProxy.ServeHTTP(w.final, w.req)
				return
			}
			rlog.Debugf("unhandled X-Accel-Redirect")
		}
	}
	w.written = true
	w.final.WriteHeader(status)
}

func route(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		log.Error("failed to extract domain from Host: %s", r.Host)
		w.WriteHeader(500)
		return
	}
	rlog := log.WithFields(log.Fields{"Host": host, "path": r.URL.Path})
	w = &ResponseWriter{w, r, rlog, false}
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
