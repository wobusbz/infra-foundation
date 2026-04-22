package model

import (
	"bytes"
	"net/http"
	"strconv"
)

type ResponseRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func NewResponseRecorder() *ResponseRecorder {
	return &ResponseRecorder{header: make(http.Header)}
}

func (r *ResponseRecorder) Header() http.Header {
	return r.header
}

func (r *ResponseRecorder) WriteHeader(statusCode int) {
	if r.status != 0 {
		return
	}
	r.status = statusCode
}

func (r *ResponseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(data)
}

func (r *ResponseRecorder) WriteTo(w http.ResponseWriter) {
	for key, values := range r.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len() > 0 {
		w.Header().Set("Content-Length", strconv.Itoa(r.body.Len()))
	}
	w.WriteHeader(r.status)
	if r.body.Len() > 0 {
		_, _ = w.Write(r.body.Bytes())
	}
}







