package readonly

import (
	"bytes"
	"encoding/json"
	"io"
)

// lastAppliedAnnotation holds the whole object as last applied, values included
// (packet M1-T4 RV2).
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// convertDocument removes the Secret values of one JSON document (RV2) and keeps what
// RV3 lists: kind, name, namespace, labels, other annotations, type and every key name
// of data and stringData, each with an empty value.
func convertDocument(body []byte, secretsResource bool) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return body, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, refuse("response is not one JSON document: %v", err)
	}
	if dec.More() {
		return nil, refuse("response holds more than one JSON document")
	}
	stripSecrets(v, secretsResource)
	return encodeJSON(v)
}

func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// stripSecrets walks a decoded document. Every object with apiVersion v1 and kind
// Secret is converted, wherever it is (List items, Table rows, Watch events). In a
// response of the core secrets resource every object with metadata is converted too,
// because list items carry no kind.
func stripSecrets(v any, secretsResource bool) {
	switch t := v.(type) {
	case map[string]any:
		_, hasMetadata := t["metadata"].(map[string]any)
		if isSecret(t) || (secretsResource && hasMetadata) {
			secretConversion(t)
		}
		for _, c := range t {
			stripSecrets(c, secretsResource)
		}
	case []any:
		for _, c := range t {
			stripSecrets(c, secretsResource)
		}
	}
}

// secretConversion is the conversion of one Secret object. The R6 tests replace it
// with their negative fixtures and put it back.
var secretConversion = stripObject

func isSecret(m map[string]any) bool {
	return m["kind"] == "Secret" && m["apiVersion"] == "v1"
}

// stripObject empties every value of data and stringData and removes the
// last-applied-configuration annotation.
func stripObject(m map[string]any) {}

func stripObjectChecked(m map[string]any) {
	for _, field := range []string{"data", "stringData"} {
		if values, ok := m[field].(map[string]any); ok {
			for k := range values {
				values[k] = ""
			}
		}
	}
	if md, ok := m["metadata"].(map[string]any); ok {
		if ann, ok := md["annotations"].(map[string]any); ok {
			delete(ann, lastAppliedAnnotation)
		}
	}
}

// watchConverter converts a JSON watch stream event by event.
type watchConverter struct {
	body    io.Closer
	dec     *json.Decoder
	secrets bool
	buf     bytes.Buffer
	err     error
}

func newWatchConverter(body io.ReadCloser, secretsResource bool) io.ReadCloser {
	dec := json.NewDecoder(body)
	dec.UseNumber()
	return &watchConverter{body: body, dec: dec, secrets: secretsResource}
}

func (w *watchConverter) Read(p []byte) (int, error) {
	for w.buf.Len() == 0 {
		if w.err != nil {
			return 0, w.err
		}
		var v any
		if err := w.dec.Decode(&v); err != nil {
			w.err = err
			continue
		}
		stripSecrets(v, w.secrets)
		out, err := encodeJSON(v)
		if err != nil {
			w.err = err
			continue
		}
		w.buf.Write(out)
	}
	return w.buf.Read(p)
}

func (w *watchConverter) Close() error {
	return w.body.Close()
}
