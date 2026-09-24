package s3

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestObjectName(t *testing.T) {
	cases := map[string]string{
		"avatars/ab/cd.png":     "avatars/ab/cd.png",
		"/avatars/cd.png":       "avatars/cd.png",
		"avatars//cd.png":       "avatars/cd.png",
		"../../etc/passwd":      "etc/passwd",
		"avatars/../../cards/x": "cards/x",
	}
	for key, want := range cases {
		got, err := objectName(key)
		if err != nil {
			t.Errorf("%q: неожиданная ошибка %v", key, err)
			continue
		}
		if got != want {
			t.Errorf("%q: получили %q, ждали %q", key, got, want)
		}
	}

	for _, key := range []string{"", "/", "..", "../"} {
		if _, err := objectName(key); !errors.Is(err, errBadKey) {
			t.Errorf("%q: ждали errBadKey, получили %v", key, err)
		}
	}
}

// Длина тела должна быть известна до загрузки: иначе клиент выделяет
// буфер под многочастную загрузку, несоразмерный нашим файлам.
func TestSized(t *testing.T) {
	data := []byte("png-bytes")

	t.Run("reader с длиной не перечитывается", func(t *testing.T) {
		src := bytes.NewReader(data)
		body, size, err := sized(src)
		if err != nil {
			t.Fatal(err)
		}
		if size != int64(len(data)) {
			t.Errorf("размер %d, ждали %d", size, len(data))
		}
		if body != io.Reader(src) {
			t.Error("reader подменён, хотя длина была известна")
		}
	})

	t.Run("reader без длины дочитывается", func(t *testing.T) {
		body, size, err := sized(io.MultiReader(strings.NewReader("png-"), strings.NewReader("bytes")))
		if err != nil {
			t.Fatal(err)
		}
		if size != int64(len(data)) {
			t.Errorf("размер %d, ждали %d", size, len(data))
		}
		got, _ := io.ReadAll(body)
		if !bytes.Equal(got, data) {
			t.Errorf("тело %q, ждали %q", got, data)
		}
	})
}

func TestURL(t *testing.T) {
	s := &Store{baseURL: "https://vc-api.example.com/files"}

	if got := s.URL(""); got != "" {
		t.Errorf("пустой ключ дал %q", got)
	}
	if got, want := s.URL("avatars/x.png"), "https://vc-api.example.com/files/avatars/x.png"; got != want {
		t.Errorf("получили %q, ждали %q", got, want)
	}
}
