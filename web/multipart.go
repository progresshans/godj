package web

import "github.com/progresshans/godj/uploads"

// Multipart parses the body once. The same policy returns the same immutable
// input; changing policy or using a released Request fails explicitly. All file
// readers and temporary content expire when the synchronous handler returns.
// Parsing and pure form validation do not authorize persistence or replace CSRF.
func (r *Request) Multipart(config uploads.Config) (*uploads.Form, error) {
	if r == nil {
		return nil, &Error{Code: CodeInvalidRequest}
	}
	r.uploadMu.Lock()
	defer r.uploadMu.Unlock()
	if !r.active.Load() || r.httpRequest == nil {
		return nil, &Error{Code: CodeInvalidRequest}
	}
	if r.uploadParsed {
		if config != r.uploadConfig {
			return nil, &uploads.Error{Code: "config_mismatch"}
		}
		return r.uploadForm, r.uploadErr
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	r.uploadParsed, r.uploadConfig = true, config
	if r.httpRequest.ContentLength > config.MaxBodyBytes {
		r.uploadErr = &uploads.Error{Code: "body_too_large"}
	} else {
		r.uploadForm, r.uploadErr = uploads.Parse(r.Context(), r.httpRequest.Body, r.httpRequest.Header.Get("Content-Type"), config)
	}
	return r.uploadForm, r.uploadErr
}
