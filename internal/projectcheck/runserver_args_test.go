//go:build darwin || linux

package projectcheck

import "testing"

func TestRunserverArgumentsAreClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		argv       []string
		descriptor string
		address    string
	}{
		{argv: []string{"runserver"}, address: defaultRunserverAddress},
		{argv: []string{"runserver", "--addr", "127.0.0.1:0"}, address: "127.0.0.1:0"},
		{argv: []string{"runserver", "--addr", "127.0.0.1:65535"}, address: "127.0.0.1:65535"},
		{argv: []string{"runserver", "--project", "godj.toml"}, descriptor: "godj.toml", address: defaultRunserverAddress},
		{argv: []string{"runserver", "--project", "nested/godj.toml", "--addr", "127.0.0.1:8080"}, descriptor: "nested/godj.toml", address: "127.0.0.1:8080"},
	}
	for _, test := range tests {
		actual, ok := parseRunserverArguments(test.argv)
		if !ok || actual.explicitDescriptor != test.descriptor || actual.address != test.address {
			t.Fatalf("parseRunserverArguments(%q) = %+v, %t", test.argv, actual, ok)
		}
	}

	invalid := [][]string{
		nil,
		{},
		{"runserver", ""},
		{"runserver", "extra"},
		{"runserver", "--project"},
		{"runserver", "--project", ""},
		{"runserver", "--project=godj.toml"},
		{"runserver", "--addr"},
		{"runserver", "--addr", ""},
		{"runserver", "--addr=127.0.0.1:8000"},
		{"runserver", "--addr", "127.0.0.1:8000", "--project", "godj.toml"},
		{"runserver", "--project", "godj.toml", "--project", "other.toml"},
		{"runserver", "--project", "godj.toml", "--addr", "127.0.0.1:8000", "extra"},
		{"runserver", "--project", "godj.toml", "--addr", "127.0.0.1:8000", "--addr", "127.0.0.1:8001"},
		{"generate"},
	}
	for _, argv := range invalid {
		if actual, ok := parseRunserverArguments(argv); ok || actual != (runserverArguments{}) {
			t.Fatalf("parseRunserverArguments(%q) = %+v, %t", argv, actual, ok)
		}
	}
}

func TestRunserverAddressIsExactLoopbackAndCanonicalPort(t *testing.T) {
	t.Parallel()
	for _, valid := range []string{
		"127.0.0.1:0",
		"127.0.0.1:1",
		"127.0.0.1:8000",
		"127.0.0.1:65535",
	} {
		if !validRunserverAddress(valid) {
			t.Fatalf("validRunserverAddress(%q) = false", valid)
		}
	}
	for _, invalid := range []string{
		"",
		"127.0.0.1",
		"127.0.0.1:",
		"127.0.0.1:+1",
		"127.0.0.1:-1",
		"127.0.0.1:00",
		"127.0.0.1:08000",
		"127.0.0.1:65536",
		"127.0.0.1:18446744073709551616",
		"127.0.0.01:8000",
		"127.0.0.2:8000",
		"localhost:8000",
		"0.0.0.0:8000",
		"[::1]:8000",
		"http://127.0.0.1:8000",
		" 127.0.0.1:8000",
		"127.0.0.1:8000 ",
		"127.0.0.1:8000\n",
	} {
		if validRunserverAddress(invalid) {
			t.Fatalf("validRunserverAddress(%q) = true", invalid)
		}
	}
}
