package domain

import "testing"

func TestNWS003UnsafeVideoURLs(t *testing.T) {
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,test", "https://youtube.com.evil.test/watch?v=abc", "https://evil.test/page", "https://user:pass@youtube.com/watch?v=abc", "https://youtube.com/embed/abc/other"} {
		if _, _, _, err := ParseVideoEmbed(raw); err == nil {
			t.Errorf("accepted unsafe video URL %q", raw)
		}
	}
}
