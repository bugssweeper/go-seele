/**
* @file
* @copyright defined in go-seele/LICENSE
 */

package types

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/seeleteam/go-seele/common"
	"github.com/seeleteam/go-seele/crypto"
	"github.com/seeleteam/go-seele/merkle"
)

const (
	defaultMaxPayloadSize = 32 * 1024
)

var (
	// ErrAmountNegative is returned when the transaction amount is negative.
	ErrAmountNegative = errors.New("amount is negative")

	// ErrAmountNil is returned when the transation amount is nil.
	ErrAmountNil = errors.New("amount is null")

	// ErrBalanceNotEnough is returned when the account balance is not enough to transfer to another account.
	ErrBalanceNotEnough = errors.New("balance not enough")

	// ErrFeeNegative is returned when the transaction fee is negative.
	ErrFeeNegative = errors.New("failed to create tx, fee is negative")

	// ErrHashMismatch is returned when the transaction hash and data mismatch.
	ErrHashMismatch = errors.New("hash mismatch")

	// ErrNonceTooLow is returned when the transaction nonce is lower than the account nonce.
	ErrNonceTooLow = errors.New("nonce too low")

	// ErrPayloadOversized is returned when the payload size is larger than the MaxPayloadSize.
	ErrPayloadOversized = errors.New("oversized payload")

	// ErrSigInvalid is returned when the transaction signature is invalid.
	ErrSigInvalid = errors.New("signature is invalid")

	// ErrSigMissing is returned when the transaction signature is missing.
	ErrSigMissing = errors.New("signature missing")

	emptyTxRootHash = crypto.MustHash("empty transaction root hash")

	// MaxPayloadSize limits the payload size to prevent malicious transactions.
	MaxPayloadSize = defaultMaxPayloadSize
)

// TransactionData wraps the data in a transaction.
type TransactionData struct {
	From         common.Address  // From is the address of the sender
	To           *common.Address // To is the receiver address, which is nil for contract creation transaction
	Amount       *big.Int        // Amount is the amount to be transferred
	AccountNonce uint64          // AccountNonce is the nonce of the sender account
	Fee          *big.Int        // Transaction Fee
	Timestamp    uint64          // Timestamp is unix nano time when the transaction is created
	Payload      []byte          // Payload is the extra data of the transaction
}

// Transaction represents a transaction in the blockchain.
type Transaction struct {
	Hash      common.Hash       // Hash is the hash of the transaction data
	Data      *TransactionData  // Data is the transaction data
	Signature *crypto.Signature // Signature is the signature of the transaction
}

// TxIndex represents an index that used to query block info by tx hash.
type TxIndex struct {
	BlockHash common.Hash
	Index     uint // tx array index in block body
}

type stateDB interface {
	GetBalance(common.Address) *big.Int
	GetNonce(common.Address) uint64
}

// NewTransaction creates a new transaction to transfer asset.
// The transaction data hash is also calculated.
// panic if the amount is nil or negative.
func NewTransaction(from, to common.Address, amount *big.Int, fee *big.Int, nonce uint64) (*Transaction, error) {
	tx, err := newTx(from, &to, amount, fee, nonce, nil)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func newTx(from common.Address, to *common.Address, amount *big.Int, fee *big.Int, nonce uint64, payload []byte) (*Transaction, error) {
	if amount == nil {
		panic("Failed to create tx, amount is nil.")
	}

	if amount.Sign() < 0 {
		panic("Failed to create tx, amount is negative.")
	}

	if fee.Sign() < 0 {
		return nil, ErrFeeNegative
	}

	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadOversized
	}

	txData := &TransactionData{
		From:         from,
		To:           to,
		Amount:       new(big.Int).Set(amount),
		Fee:          new(big.Int).Set(fee),
		Timestamp:    uint64(time.Now().UnixNano()),
		AccountNonce: nonce,
	}

	if len(payload) > 0 {
		cloned := make([]byte, len(payload))
		copy(cloned, payload)
		txData.Payload = cloned
	} else {
		txData.Payload = make([]byte, 0)
	}

	return &Transaction{crypto.MustHash(txData), txData, nil}, nil
}

// NewContractTransaction returns a transaction to create a smart contract.
func NewContractTransaction(from common.Address, amount *big.Int, fee *big.Int, nonce uint64, code []byte) (*Transaction, error) {
	return newTx(from, nil, amount, fee, nonce, code)
}

// NewMessageTransaction returns a transation with the specified message.
func NewMessageTransaction(from, to common.Address, amount *big.Int, fee *big.Int, nonce uint64, msg []byte) (*Transaction, error) {
	return newTx(from, &to, amount, fee, nonce, msg)
}

// Sign signs the transaction with the specified private key.
func (tx *Transaction) Sign(privKey *ecdsa.PrivateKey) {
	tx.Hash = crypto.MustHash(tx.Data)
	tx.Signature = crypto.NewSignature(privKey, tx.Hash.Bytes())
}

// Validate returns true if the transaction is valid, otherwise false.
func (tx *Transaction) Validate(statedb stateDB) error {
	if tx.Data == nil || tx.Data.Amount == nil {
		return ErrAmountNil
	}

	if tx.Data.Amount.Sign() < 0 {
		return ErrAmountNegative
	}

	if fromShardNum := common.GetShardNumber(tx.Data.From); fromShardNum != common.LocalShardNumber {
		return fmt.Errorf("invalid from address, shard number is [%v], but coinbase shard number is [%v]", fromShardNum, common.LocalShardNumber)
	}

	if tx.Data.To != nil {
		if toShardNum := common.GetShardNumber(*tx.Data.To); toShardNum != common.LocalShardNumber {
			return fmt.Errorf("invalid to address, shard number is [%v], but coinbase shard number is [%v]", toShardNum, common.LocalShardNumber)
		}
	}

	if balance := statedb.GetBalance(tx.Data.From); tx.Data.Amount.Cmp(balance) > 0 {
		return ErrBalanceNotEnough
	}

	if accountNonce := statedb.GetNonce(tx.Data.From); tx.Data.AccountNonce < accountNonce {
		return ErrNonceTooLow
	}

	if len(tx.Data.Payload) > MaxPayloadSize {
		return ErrPayloadOversized
	}

	if tx.Signature == nil {
		return ErrSigMissing
	}

	txDataHash := crypto.MustHash(tx.Data)
	if !txDataHash.Equal(tx.Hash) {
		return ErrHashMismatch
	}

	if !tx.Signature.Verify(&tx.Data.From, txDataHash.Bytes()) {
		return ErrSigInvalid
	}

	return nil
}

// CalculateHash calculates and returns the transaction hash.
// This is to implement the merkle.Content interface.
func (tx *Transaction) CalculateHash() common.Hash {
	return crypto.MustHash(tx.Data)
}

// Equals indicates if the transaction is equal to the specified content.
// This is to implement the merkle.Content interface.
func (tx *Transaction) Equals(other merkle.Content) bool {
	otherTx, ok := other.(*Transaction)
	return ok && tx.Hash.Equal(otherTx.Hash)
}

// MerkleRootHash calculates and returns the merkle root hash of the specified transactions.
// If the given transactions are empty, return empty hash.
func MerkleRootHash(txs []*Transaction) common.Hash {
	if len(txs) == 0 {
		return emptyTxRootHash
	}

	contents := make([]merkle.Content, len(txs))
	for i, tx := range txs {
		contents[i] = tx
	}

	bmt, _ := merkle.NewTree(contents)

	return bmt.MerkleRoot()
}
