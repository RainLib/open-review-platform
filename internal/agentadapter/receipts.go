package agentadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/google/uuid"
)

// receiptStore retains a single adapter replica's job identity across a
// process restart. It is not a distributed work queue or an execution sandbox.
type receiptStore struct {
	dir  string
	lock *os.File
}

type persistedReceipt struct {
	JobID       string                        `json:"job_id"`
	AttemptID   string                        `json:"attempt_id"`
	Digest      string                        `json:"digest"`
	Submission  Submission                    `json:"submission"`
	Status      string                        `json:"status"`
	Publication *PublicationCheckpoint        `json:"publication,omitempty"`
	Terminal    *domain.AgentTaskAdapterEvent `json:"terminal,omitempty"`
	Delivered   bool                          `json:"delivered"`
	Rejected    bool                          `json:"rejected"`
	CreatedAt   time.Time                     `json:"created_at"`
}

func openReceiptStore(dir string) (*receiptStore, error) {
	if dir == "" {
		return nil, nil
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("adapter receipt directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create adapter receipt directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("adapter receipt directory must be a private real directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, fmt.Errorf("make adapter receipt directory private: %w", err)
		}
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".adapter.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open adapter receipt lock: %w", err)
	}
	lockInfo, err := lock.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() || lockInfo.Mode().Perm()&0o077 != 0 {
		_ = lock.Close()
		return nil, fmt.Errorf("adapter receipt lock must be a private regular file")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("another adapter owns the receipt directory: %w", err)
	}
	return &receiptStore{dir: dir, lock: lock}, nil
}

func (store *receiptStore) close() error {
	if store == nil || store.lock == nil {
		return nil
	}
	err := syscall.Flock(int(store.lock.Fd()), syscall.LOCK_UN)
	closeErr := store.lock.Close()
	store.lock = nil
	if err != nil {
		return err
	}
	return closeErr
}

func (store *receiptStore) path(attemptID string) (string, error) {
	if _, err := uuid.Parse(attemptID); err != nil || strings.ContainsAny(attemptID, "/\\") {
		return "", fmt.Errorf("adapter receipt attempt ID is invalid")
	}
	return filepath.Join(store.dir, attemptID+".json"), nil
}

func (store *receiptStore) save(record persistedReceipt) error {
	if store == nil {
		return nil
	}
	target, err := store.path(record.AttemptID)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil || len(encoded) > 256<<10 {
		return fmt.Errorf("adapter receipt exceeds its bounded format")
	}
	temporary, err := os.CreateTemp(store.dir, ".receipt-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(encoded)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(temporary.Name(), target); err != nil {
		return err
	}
	return store.syncDirectory()
}

func (store *receiptStore) remove(attemptID string) error {
	if store == nil {
		return nil
	}
	target, err := store.path(attemptID)
	if err != nil {
		return err
	}
	if err = os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return store.syncDirectory()
}

func (store *receiptStore) syncDirectory() error {
	directory, err := os.Open(store.dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (store *receiptStore) load() ([]persistedReceipt, error) {
	if store == nil {
		return nil, nil
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		return nil, err
	}
	records := make([]persistedReceipt, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		attemptID := strings.TrimSuffix(entry.Name(), ".json")
		path, err := store.path(attemptID)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 256<<10 {
			return nil, fmt.Errorf("adapter receipt is not a bounded regular file")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		var record persistedReceipt
		decoder := json.NewDecoder(io.LimitReader(file, (256<<10)+1))
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&record)
		if decodeErr == nil {
			decodeErr = decoder.Decode(&struct{}{})
			if decodeErr == io.EOF {
				decodeErr = nil
			}
		}
		_ = file.Close()
		if decodeErr != nil || record.AttemptID != attemptID || record.JobID == "" || record.CreatedAt.IsZero() || !validReceiptStatus(record.Status) {
			return nil, fmt.Errorf("adapter receipt %s is invalid", entry.Name())
		}
		digest, err := hex.DecodeString(record.Digest)
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("adapter receipt %s has an invalid digest", entry.Name())
		}
		records = append(records, record)
	}
	return records, nil
}

func validReceiptStatus(status string) bool {
	switch status {
	case "reserved", "starting", "started", "cancelled", "terminal":
		return true
	default:
		return false
	}
}
