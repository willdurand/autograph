package database

import (
	"database/sql"
	"time"

	"github.com/pkg/errors"
)

var (
	// ErrNoSuitableEEFound is returned when no suitable key is found in database
	ErrNoSuitableEEFound = errors.New("no suitable key found in database")
)

// GetLabelOfLatestEE returns the label of the latest end-entity for the specified signer
// that is no older than a given duration
func (db *Handler) GetLabelOfLatestEE(signerID string, youngerThan time.Duration) (label, x5u string, err error) {
	var nullableX5U sql.NullString
	maxAge := time.Now().Add(-youngerThan)
	err = db.QueryRow(`SELECT label, x5u FROM endentities
				WHERE is_current=TRUE AND signer_id=$1 AND created_at > $2
				ORDER BY created_at DESC LIMIT 1`,
		signerID, maxAge).Scan(&label, &nullableX5U)
	if err == sql.ErrNoRows {
		return "", "", ErrNoSuitableEEFound
	}
	x5uValue, err := nullableX5U.Value()
	if x5uValue != nil {
		x5u = x5uValue.(string)
	}
	return
}

// BeginEndEntityOperations creates a database transaction that locks the endentities table,
// this should be called before doing any lookup or generation operation with endentities.
//
// This global lock will effectively prevent any sort of concurrent operation, which is exactly
// what we want in the case of key generation. Being slow and blocking is OK, risking two
// key generation the happen in parallel is not.
func (db *Handler) BeginEndEntityOperations() (*Transaction, error) {
	// if a db is present, first create a db transaction to lock the row for update
	tx, err := db.Begin()
	if err != nil {
		err = errors.Wrap(err, "failed to create transaction")
		tx.Rollback()
		return nil, err
	}
	// lock the table
	_, err = tx.Exec("LOCK TABLE endentities IN ROW EXCLUSIVE MODE")
	if err != nil {
		err = errors.Wrap(err, "failed to lock endentities table")
		tx.Rollback()
		return nil, err
	}
	return &Transaction{tx}, nil
}

// InsertEE uses an existing transaction to insert an end-entity in database
func (tx *Transaction) InsertEE(x5u, label, signerID string, hsmHandle uint) (err error) {
	_, err = tx.Exec(`INSERT INTO endentities(x5u, label, signer_id, hsm_handle, is_current, created_at)
				VALUES ($1, $2, $3, $4, $5, $6)`, x5u, label, signerID, hsmHandle, true, time.Now().UTC())
	if err != nil {
		tx.Rollback()
		err = errors.Wrap(err, "failed to insert new key in database")
		return
	}
	// mark all other keys for this signer as no longer current
	_, err = tx.Exec("UPDATE endentities SET is_current=FALSE WHERE signer_id=$1 and label!=$2",
		signerID, label)
	if err != nil {
		err = errors.Wrap(err, "failed to update is_current status of keys in database")
		tx.Rollback()
		return
	}
	return nil
}

// End commits a transaction
func (tx *Transaction) End() error {
	err := tx.Commit()
	if err != nil {
		err = errors.Wrap(err, "failed to commit transaction in database")
		tx.Rollback()
		return err
	}
	return nil
}
