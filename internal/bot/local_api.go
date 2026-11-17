package bot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const movedDir = "/var/lib/yu-botapi-moved"

func ensureLocalBot(token string) error {
	if err := os.MkdirAll(movedDir, 0o1777); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(token))
	file, err := os.OpenFile(filepath.Join(movedDir, hex.EncodeToString(sum[:])), os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	mark := make([]byte, 1)
	if n, _ := file.Read(mark); n == 1 {
		return nil
	}
	response, err := http.Post("https://api.telegram.org/bot"+token+"/logOut", "", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK && !strings.Contains(strings.ToLower(string(body)), "logged out") {
		return fmt.Errorf("telegram logout failed: %s", response.Status)
	}
	_, err = file.WriteAt([]byte("1"), 0)
	return err
}
