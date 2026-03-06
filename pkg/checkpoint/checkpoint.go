package checkpoint

import (
	"errors"
	"os"
	"path"
	"strconv"
	"strings"
)

type Checkpoint struct {
	basePath string
	seq      uint64
}

func NewCheckpoint(basePath string) (*Checkpoint, error) {
	cp := &Checkpoint{basePath: basePath}

	data, err := os.ReadFile(path.Join(basePath, "checkpoint.seq"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(data) > 0 {
		cp.seq, _ = strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	}
	return cp, nil
}

func (cp *Checkpoint) Save(seq uint64) error {
	// 寫到 tmp 再 rename，確保原子性，防止寫到一半 crash 導致檔案損壞
	tmp := path.Join(cp.basePath, "checkpoint.seq.tmp")
	finalPath := path.Join(cp.basePath, "checkpoint.seq")
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(seq, 10)), 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, finalPath); err != nil {
		return err
	}
	cp.seq = seq
	return nil
}

func (cp *Checkpoint) LastSeq() uint64 {
	return cp.seq
}
