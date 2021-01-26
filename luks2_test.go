package luks

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func prepareLuks2Disk(t *testing.T, password string, cryptsetupArgs ...string) (*os.File, error) {
	disk, err := ioutil.TempFile("", "luksv2.go.disk")
	if err != nil {
		t.Fatal(err)
	}

	if err := disk.Truncate(24 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}

	args := []string{"luksFormat", "--type", "luks2", "--iter-time", "5", "-q", disk.Name()}
	args = append(args, cryptsetupArgs...)
	cmd := exec.Command("cryptsetup", args...)
	cmd.Stdin = strings.NewReader(password)
	if testing.Verbose() {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return disk, err
}

// TODO: test custom --sector-size
func runLuks2Test(t *testing.T, cryptsetupArgs ...string) {
	t.Parallel()

	password := "foobar"
	disk, err := prepareLuks2Disk(t, password, cryptsetupArgs...)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	defer os.Remove(disk.Name())

	d, err := initV2Device(disk.Name(), disk)
	if err != nil {
		t.Fatal(err)
	}

	uuid, err := blkdidUuid(disk.Name())
	if err != nil {
		t.Fatal(err)
	}
	if d.Uuid() != uuid {
		t.Fatalf("wrong UUID: expected %s, got %s", uuid, d.Uuid())
	}

	if _, err := d.decryptKeyslot(0, []byte(password)); err != nil {
		t.Fatal(err)
	}
}

func TestLuks2UnlockBasic(t *testing.T) {
	runLuks2Test(t)
}

func TestLuks2UnlockSha3(t *testing.T) {
	runLuks2Test(t, "--perf-no_read_workqueue", "--perf-no_write_workqueue", "--cipher", "aes-xts-plain64", "--key-size", "512", "--iter-time", "2000", "--pbkdf", "argon2id", "--hash", "sha3-512")
}

func TestLuks2UnlockMultipleKeySlots(t *testing.T) {
	t.Parallel()

	password := "barfoo"
	disk, err := prepareLuks2Disk(t, password)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	defer os.Remove(disk.Name())

	// now let's add a new keyslot and try to unlock again
	addKeyCmd := exec.Command("cryptsetup", "luksAddKey", "-q", disk.Name())
	password2 := "newpwd"
	addKeyCmd.Stdin = strings.NewReader(password + "\n" + password2)
	if testing.Verbose() {
		addKeyCmd.Stdout = os.Stdout
		addKeyCmd.Stderr = os.Stderr
	}
	if err := addKeyCmd.Run(); err != nil {
		t.Fatal(err)
	}

	d, err := initV2Device(disk.Name(), disk)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := d.decryptKeyslot(0, []byte(password)); err != nil {
		t.Fatal(err)
	}
	if _, err := d.decryptKeyslot(1, []byte(password2)); err != nil {
		t.Fatal(err)
	}
}

func TestLuks2UnlockWithToken(t *testing.T) {
	t.Parallel()

	password := "foobar"
	disk, err := prepareLuks2Disk(t, password)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	defer os.Remove(disk.Name())

	addTokenCmd := exec.Command("cryptsetup", "token", "import", disk.Name())
	slotId := 0
	payload := fmt.Sprintf(`{"type":"clevis","keyslots":["%d"],"jwe":{"ciphertext":"","encrypted_key":"","iv":"","protected":"test\n","tag":""}}`, slotId)
	addTokenCmd.Stdin = strings.NewReader(payload)
	if testing.Verbose() {
		addTokenCmd.Stdout = os.Stdout
		addTokenCmd.Stderr = os.Stderr
	}
	if err := addTokenCmd.Run(); err != nil {
		t.Fatal(err)
	}

	d, err := initV2Device(disk.Name(), disk)
	if err != nil {
		t.Fatal(err)
	}

	slots := d.Slots()
	if len(slots) != 1 && slots[0] != 0 {
		t.Fatalf("Invalid slots data")
	}

	tokens, err := d.Tokens()
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 1 {
		t.Fatalf("Expected 1 token, got %d", len(tokens))
	}
	tk := tokens[0]
	if tk.Type != ClevisTokenType {
		t.Fatalf("Expected clevis token type, got %d", tk.Type)
	}
	if !reflect.DeepEqual(tk.Slots, []int{0}) {
		t.Fatalf("Expected '0' slotid, got %+v", tk.Slots)
	}

	expected := `{"type":"clevis","keyslots":["0"],"jwe":{"ciphertext":"","encrypted_key":"","iv":"","protected":"test\n","tag":""}}`
	p := string(tk.Payload)
	if p != expected {
		t.Fatalf("Invalid token payload received, expected '%s', got '%s'", expected, p)
	}

	uuid, err := blkdidUuid(disk.Name())
	if err != nil {
		t.Fatal(err)
	}
	if d.Uuid() != uuid {
		t.Fatalf("wrong UUID: expected %s, got %s", uuid, d.Uuid())
	}

	if _, err := d.decryptKeyslot(0, []byte(password)); err != nil {
		t.Fatal(err)
	}
}
