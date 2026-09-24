package config

import "testing"

// Origin сравнивается с заголовком браузера точным совпадением строк,
// поэтому любая «почти правильная» запись должна падать на старте,
// а не молча выключать CORS для фронта.
func TestValidateOrigin(t *testing.T) {
	valid := []string{
		"https://vc.knyazushka.ru",
		"http://localhost:3000",
	}
	for _, origin := range valid {
		if err := validateOrigin(origin); err != nil {
			t.Errorf("%q: неожиданная ошибка %v", origin, err)
		}
	}

	invalid := map[string]string{
		"слэш в конце":         "https://vc.knyazushka.ru/",
		"путь":                 "https://vc.knyazushka.ru/app",
		"верхний регистр":      "https://VC.knyazushka.ru",
		"маска":                "*",
		"без схемы":            "vc.knyazushka.ru",
		"чужая схема":          "ftp://vc.knyazushka.ru",
		"пробел после запятой": " https://vc.knyazushka.ru",
	}
	for name, origin := range invalid {
		if err := validateOrigin(origin); err == nil {
			t.Errorf("%s (%q): ошибки нет", name, origin)
		}
	}
}

func TestS3RequiredOnlyForS3Driver(t *testing.T) {
	base := Config{
		DB:     DB{MaxConns: 10, MinConns: 2},
		Auth:   Auth{JWTSecret: "0123456789abcdef0123456789abcdef"},
		Public: Public{AppURL: "https://vc.knyazushka.ru"},
	}

	local := base
	local.Storage.Driver = "local"
	if err := local.validate(); err != nil {
		t.Errorf("local без настроек S3: %v", err)
	}

	s3 := base
	s3.Storage.Driver = "s3"
	if err := s3.validate(); err == nil {
		t.Error("s3 без настроек прошёл проверку")
	}

	s3.Storage.S3 = S3{
		Endpoint:  "https://s3.example.com",
		Bucket:    "vcard",
		AccessKey: "key",
		SecretKey: "secret",
	}
	if err := s3.validate(); err != nil {
		t.Errorf("полностью настроенный s3: %v", err)
	}

	s3.Storage.S3.Endpoint = "s3.example.com"
	if err := s3.validate(); err == nil {
		t.Error("адрес без схемы прошёл проверку")
	}
}
