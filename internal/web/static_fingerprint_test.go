package web

import (
	"testing"
	"testing/fstest"
)

func TestStaticFingerprint_ChangesWhenAnAssetsBytesChange(t *testing.T) {
	a := fstest.MapFS{"app.css": {Data: []byte("body{color:red}")}, "copy.js": {Data: []byte("x")}}
	b := fstest.MapFS{"app.css": {Data: []byte("body{color:blue}")}, "copy.js": {Data: []byte("x")}}
	c := fstest.MapFS{"app.css": {Data: []byte("body{color:red}")}, "copy.js": {Data: []byte("x")}}
	fa, fb, fc := computeStaticFingerprint(a), computeStaticFingerprint(b), computeStaticFingerprint(c)
	if fa == fb {
		t.Errorf("fingerprint %s did not change when app.css changed", fa)
	}
	if fa != fc {
		t.Errorf("fingerprint is not stable for identical bytes: %s vs %s", fa, fc)
	}
	if len(fa) != 12 {
		t.Errorf("fingerprint %q, want 12 hex", fa)
	}
}
