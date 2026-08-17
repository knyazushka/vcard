package auth

import (
	"strings"
	"testing"
)

// Параметры ниже намеренно слабее боевых: тесты не должны тратить
// по 60 МБ и десятки миллисекунд на каждый вызов.
func testHasher() *Hasher { return NewHasher(8*1024, 1, 1) }

func TestHashVerifyRoundTrip(t *testing.T) {
	h := testHasher()

	encoded, err := h.Hash("correct-horse-battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := h.Verify("correct-horse-battery", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("правильный пароль не принят")
	}

	ok, err = h.Verify("wrong-password", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("неправильный пароль принят")
	}
}

// Соль должна быть разной у каждого хэша, иначе одинаковые пароли дают
// одинаковые строки и по базе сразу видно, у кого пароли совпадают.
func TestHashIsSalted(t *testing.T) {
	h := testHasher()

	first, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	second, err := h.Hash("same-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	if first == second {
		t.Fatal("два хэша одного пароля совпали — соль не работает")
	}
}

// Длинный пароль должен участвовать в проверке целиком. Именно на этом
// ломается bcrypt: он молча обрезает вход на 72 байтах, и пароли,
// различающиеся только хвостом, становятся взаимозаменяемыми.
func TestLongPasswordNotTruncated(t *testing.T) {
	h := testHasher()

	base := strings.Repeat("a", 72)
	encoded, err := h.Hash(base + "TAIL-ONE")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := h.Verify(base+"TAIL-TWO", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("пароли, различающиеся после 72-го байта, признаны одинаковыми")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	h := testHasher()

	for _, encoded := range []string{
		"",
		"not-a-hash",
		"$argon2i$v=19$m=8,t=1,p=1$c2FsdA$a2V5", // другой вариант argon2
		"$argon2id$v=19$m=8,t=1,p=1$!!!$!!!",    // битый base64
	} {
		if _, err := h.Verify("whatever", encoded); err == nil {
			t.Errorf("битый хэш %q принят без ошибки", encoded)
		}
	}
}

// Параметры лежат внутри строки хэша, поэтому ужесточение настроек
// не обесценивает уже сохранённые пароли: они проверяются со своими.
func TestVerifyUsesParamsFromHash(t *testing.T) {
	weak := NewHasher(8*1024, 1, 1)
	strong := NewHasher(32*1024, 3, 2)

	encoded, err := weak.Hash("password-value")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	ok, err := strong.Verify("password-value", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("хэш, посчитанный со старыми параметрами, перестал проверяться")
	}
}
