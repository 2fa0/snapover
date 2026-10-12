package clip

import "testing"

func TestTextNewlines(t *testing.T) {
	const lf = "  你好\n\n\tworld\rvalue\n"
	const crlf = "  你好\r\n\r\n\tworld\rvalue\r\n"
	for _, input := range []string{lf, crlf, "  你好\r\n\n\tworld\rvalue\r\n"} {
		item, err := FromText(input)
		if err != nil {
			t.Fatal(err)
		}
		if item.Text != lf || item.Size != len(lf) || item.Hash != HashText(lf) {
			t.Fatalf("FromText(%q) = %+v", input, item)
		}
		if HashText(input) != item.Hash {
			t.Fatal("hash depends on newline format")
		}
		for _, platform := range []string{"darwin", "windows"} {
			want := lf
			if platform == "windows" {
				want = crlf
			}
			got := clipboardText(input, platform)
			if got != want || clipboardText(got, platform) != want {
				t.Fatalf("clipboardText(%q, %q) = %q, want %q", input, platform, got, want)
			}
			guard := NewEchoGuard(0)
			guard.Mark(item.Hash)
			readBack, err := FromText(got)
			if err != nil {
				t.Fatal(err)
			}
			if !guard.IsEcho(readBack.Hash) {
				t.Fatalf("%s clipboard round trip was not recognized as echo", platform)
			}
		}
	}
}
