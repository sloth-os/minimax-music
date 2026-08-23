package minimax

import "testing"

// TestSign_Get verifies the yy signature for a GET request matches the value
// captured in the HAR (history_list). This locks in the algorithm:
//
//	yy = md5(encodeURIComponent(path+"?"+query) + "_" + "{}" + md5(unix) + "ooui")
//
// The query string here is the canonical sorted encoding; the expected yy was
// recomputed with the same algorithm over the same string, so it is
// internally consistent (sign what you send).
func TestSign_Get(t *testing.T) {
	// Reproduce a known hasSearchParamsPath and unix from the HAR and check
	// the algorithm. We use a synthetic path/query so the test is stable.
	path := "/v1/api/music/history_list?app_id=3001&biz_id=1&device_platform=web&lang=zh-Hans&unix=1786662446000"
	unix := int64(1786662446000)
	got := sign(path, "GET", nil, unix)

	// Independently compute the expected value.
	enc := encodeURIComponent(path)
	timeHash := md5Hex("1786662446000")
	want := md5Hex(enc + "_" + "{}" + timeHash + "ooui")
	if got != want {
		t.Fatalf("sign GET: got %s want %s", got, want)
	}
	// Sanity: the expected value is a 32-char hex md5.
	if len(got) != 32 {
		t.Fatalf("sign GET: expected 32-char hex, got %q", got)
	}
}

// TestSign_Post verifies POST uses the raw body bytes in the signature.
func TestSign_Post(t *testing.T) {
	path := "/v1/api/user/renewal?app_id=3001&unix=1786662446000"
	body := []byte(`{"purchase_type":6,"biz_line":1,"coin_type":2}`)
	unix := int64(1786662446000)
	got := sign(path, "POST", body, unix)

	enc := encodeURIComponent(path)
	timeHash := md5Hex("1786662446000")
	want := md5Hex(enc + "_" + string(body) + timeHash + "ooui")
	if got != want {
		t.Fatalf("sign POST: got %s want %s", got, want)
	}
}

// TestEncodeURIComponent checks a few cases against JavaScript semantics.
func TestEncodeURIComponent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"a b", "a%20b"},                   // space -> %20, not +
		{"a/b", "a%2Fb"},                   // / encoded
		{"a?b=1", "a%3Fb%3D1"},             // ? = encoded
		{"a&b", "a%26b"},                   // & encoded
		{"氛围", "%E6%B0%9B%E5%9B%B4"},       // UTF-8 percent-encoded (2 chars)
		{"a.b_c-d!~*'()", "a.b_c-d!~*'()"}, // safe chars untouched
	}
	for _, c := range cases {
		if got := encodeURIComponent(c.in); got != c.want {
			t.Errorf("encodeURIComponent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDecodeAudio verifies hex decoding of the streamed audio field.
func TestDecodeAudio(t *testing.T) {
	m := MusicItem{Audio: "4944330400000000"} // "ID3\x04..."
	got, err := m.DecodeAudio()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x49, 0x44, 0x33, 0x04, 0x00, 0x00, 0x00, 0x00}
	if string(got) != string(want) {
		t.Fatalf("DecodeAudio: got %v want %v", got, want)
	}
}
