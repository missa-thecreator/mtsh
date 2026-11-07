package osinfo

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name, out string
		want      Info
	}{
		{"ubuntu", "PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\nID_LIKE=debian\n\nLinux\n",
			Info{ID: "ubuntu", Like: "debian", Name: "Ubuntu 24.04"}},
		{"debian", "NAME=\"Debian GNU/Linux\"\nVERSION_ID=\"12\"\nID=debian\n\nLinux\n",
			Info{ID: "debian", Name: "Debian 12"}},
		{"alpine sem versão", "NAME=\"Alpine Linux\"\nID=alpine\n\nLinux\n",
			Info{ID: "alpine", Name: "Alpine"}},
		{"rocky", "NAME=\"Rocky Linux\"\nVERSION_ID=\"9.4\"\nID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n\nLinux\n",
			Info{ID: "rocky", Like: "rhel", Name: "Rocky 9.4"}},
		{"macos", "\nDarwin\n", Info{ID: "macos", Name: "macOS"}},
	}
	for _, c := range cases {
		got, ok := Parse(c.out)
		if !ok || got != c.want {
			t.Errorf("%s: got %+v (ok=%v), want %+v", c.name, got, ok, c.want)
		}
	}
	if _, ok := Parse("bash: cat: command not found\n"); ok {
		t.Error("saída sem sistema não deveria ser reconhecida")
	}
}
