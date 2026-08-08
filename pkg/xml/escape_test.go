package xml

import "testing"

func TestAppendEscapedSanitized(t *testing.T) {
	got := string(AppendEscapedSanitized(nil, `a<&"'&>`))
	want := "a&lt;&amp;&quot;&#39;&amp;&gt;"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestInvalidXMLRunesFiltered(t *testing.T) {
	input := "a\x00b\x07c\x1fd\x7fe\u0085f\ufeffg\ufffeh\uffffi\tj\nk\rl"
	got := string(AppendEscapedSanitized(nil, input))
	want := "abcdefghi\tj\nk\rl"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
