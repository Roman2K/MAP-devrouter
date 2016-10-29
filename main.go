package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	log "gopkg.in/Sirupsen/logrus.v0"

	"github.com/Roman2K/MAP-devrouter/filetest"
)

// config
var (
	addr            = flag.String("addr", ":8080", "Address to listen on")
	mapURL          = flag.String("map", "http://localhost:3000", "MAP (ecommerce) server URL")
	mapPublicDir    = flag.String("map-public", "~/code/map/map/public", "Path to public dir of MAP")
	editorURL       = flag.String("editor", "http://localhost:3001", "Editor server URL")
	editorPublicDir = flag.String("editor-public", "~/code/map/editor/public", "Path to public dir of Editor")
	storageURL      = flag.String("storage", "http://localhost:3010", "Storage server URL")
)

// package globals
var (
	mapProxy                 http.Handler
	editorProxy              http.Handler
	storageProxy             http.Handler
	uploadsCMSRe             *regexp.Regexp
	mapPublic                fileServer
	editorPublic             fileServer
	nop                      []*regexp.Regexp
	xAccelRedirectHopHeaders = []string{
		"X-Accel-Redirect",
		"Content-Type",
		"Content-Length",
	}
)

func init() {
	uploadsCMSRe = regexp.MustCompile(`^/uploads_cms/(\w+)-(\w+)-(\d+)/(.+)$`)
	nop = append(nop,
		regexp.MustCompile(`/favicon\.ico$`),
		regexp.MustCompile(`^/mini-profiler`),
	)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	flag.Parse()
	mapProxy = newReverseProxy(*mapURL)
	editorProxy = newReverseProxy(*editorURL)
	storageProxy = newReverseProxy(*storageURL)

	var err error
	mapPublic, err = newFileServer(expandHome(*mapPublicDir))
	if err != nil {
		panic(err)
	}
	editorPublic, err = newFileServer(expandHome(*editorPublicDir))
	if err != nil {
		panic(err)
	}

	log.SetLevel(log.DebugLevel)
	log.Infof("MAP: proxy to %s", *mapURL)
	log.Infof("MAP: serve public %s", *mapPublicDir)
	log.Infof("Editor: proxy to %s", *editorURL)
	log.Infof("Editor: serve public %s", *editorPublicDir)
	log.Infof("Storage: proxy to %s", *storageURL)
	log.Infof("listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, http.HandlerFunc(route)))
}

func expandHome(path string) string {
	home := os.Getenv("HOME")
	if home == "" {
		panic("$HOME not set")
	}
	return strings.Replace(path, "~", home, -1)
}

type fileServer struct {
	dir     string
	handler http.Handler
}

func newFileServer(dir string) (h fileServer, err error) {
	ok, err := filetest.IsDir(dir)
	if err != nil {
		return
	}
	if !ok {
		err = fmt.Errorf("%q not a directory", dir)
		return
	}
	h = fileServer{
		dir:     dir,
		handler: http.FileServer(http.Dir(dir)),
	}
	return
}

func (h fileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}

func newReverseProxy(urlstr string) http.Handler {
	url, err := url.Parse(urlstr)
	if err != nil {
		panic(err)
	}
	return httputil.NewSingleHostReverseProxy(url)
}

func route(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		log.Errorf("failed to extract domain from Host %q: %v", r.Host, err)
		w.WriteHeader(500)
		return
	}

	rlog := log.WithFields(log.Fields{"Host": host, "path": r.URL.Path})

	switch host {
	case "api.map.dev":
		hlog := rlog.WithField("app", "MAP API")
		hlog.Info("reverse-proxying")
		mapProxy.ServeHTTP(w, r)
		return
	case "app.map.dev":
		hlog := rlog.WithField("app", "Editor")
		tryFile(w, r, editorPublic, editorProxy, hlog)
		return
	case "map.dev":
		hlog := rlog.WithField("app", "MAP")
		if m := uploadsCMSRe.FindStringSubmatch(r.URL.Path); m != nil {
			//
			// Example URL:
			// /uploads_cms/block_taxon-image-610/livre-photo.png
			//
			// Matched with:
			// ^/uploads_cms/(\w+)-(\w+)-(\d+)/(.+)$
			//
			// File:
			// public/uploads/cms/block_taxon/image/610/livre-photo.png
			//
			filePath := fmt.Sprintf("/uploads/cms/%s/%s/%s/%s", m[1], m[2], m[3], m[4])
			ulog := hlog.WithField("file", filePath)
			ulog.Info("sending uploads_cms")
			r.URL.Path = filePath
			mapPublic.ServeHTTP(w, r)
			return
		}
		w = responseWriter{w, r, false, rlog} // support X-Accel-Redirect to /storage
		tryFile(w, r, mapPublic, mapProxy, hlog)
		return
	}

	if isNop(r.URL.Path) {
		rlog.Info("nop")
		http.NotFound(w, r)
		return
	}

	rlog.Errorf("unknown Host")
	http.NotFound(w, r)
}

func tryFile(w http.ResponseWriter, r *http.Request, public fileServer, app http.Handler, log *log.Entry) {
	ok, err := filetest.IsFile(filepath.Join(public.dir, r.URL.Path))
	if err != nil {
		log.Errorf("while testing file: %v", err)
		w.WriteHeader(500)
		return
	}
	if ok {
		log.Info("sending public file")
		public.ServeHTTP(w, r)
		return
	}
	log.Info("reverse-proxying")
	app.ServeHTTP(w, r)
}

func isNop(path string) bool {
	for _, re := range nop {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

type responseWriter struct {
	res     http.ResponseWriter
	req     *http.Request
	written bool
	log     *log.Entry
}

func (w responseWriter) Header() http.Header {
	return w.res.Header()
}

func (w responseWriter) Write(chunk []byte) (int, error) {
	w.written = true
	return w.res.Write(chunk)
}

func (w responseWriter) WriteHeader(status int) {
	if redir := w.res.Header().Get("X-Accel-Redirect"); redir != "" {
		rlog := w.log.WithFields(log.Fields{"X-Accel-Redirect": redir})
		if w.written {
			rlog.Errorf("attempted to X-Accel-Redirect after write")
			return
		}
		if path := strings.TrimPrefix(redir, "/storage/"); path != redir {
			hlog := rlog.WithField("app", "Storage")
			hlog.Infof("reverse-proxying")
			w.req.URL.Path = path
			for _, key := range xAccelRedirectHopHeaders {
				w.Header().Del(key)
			}
			w.written = true
			storageProxy.ServeHTTP(w.res, w.req)
			return
		}
		rlog.Debugf("unhandled X-Accel-Redirect")
	}
	w.written = true
	w.res.WriteHeader(status)
}
