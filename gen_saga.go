package ergo

import (
	"fmt"

	"github.com/halturin/ergo/etf"
)

type GenSaga struct {
	GenServer
}

type GenSagaTransactionOptions struct {
	// Name defines the name of this transaction. By default
	// this name has autogenerated ID.
	Name string
	// IgnoreLoop whether to cancel the transaction if a loop was detected.
	// Default is false.
	IgnoreLoop bool
	// HopLimit defines a number of hop within the transaction. Default limit
	// is 0 (no limit).
	HopLimit uint
}

type GenSagaOptions struct {
	// MaxTransactions defines the limit of active transactions.
	MaxTransactions int
}

type GenSagaState struct {
	GenServerState
	options  GenSagaOptions
	txs      map[string]GenSagaTransaction
	internal interface{}
}

type GenSagaTransaction struct {
	Options GenSagaTransactionOptions
	Name    string
	Pid     etf.Pid
	Ref     etf.Ref
	parents []etf.Pid
}

// GenSagaBehaviour interface
type GenSagaBehaviour interface {
	//
	// Mandatory callbacks
	//

	// InitSaga
	InitSaga(process *Process, args ...interface{}) (GenSagaOptions, interface{})

	// HandleCancel invoked on a request of transaction cancelation.
	HandleCancel(tx GenSagaTransaction, state GenSagaState) error

	// HandleCanceled invoked if the given transaction has been canceled by some
	// reason (node or process went down or by explicit cancelation).
	HandleCanceled(tx GenSagaTransaction, reason string, state GenSagaState) error

	// HandleDone
	HandleDone(tx GenSagaTransaction, result interface{}, state GenSagaState) error

	//
	// Optional callbacks
	//

	HandleNext(tx GenSagaTransaction, arg interface{}, state GenSagaState) error
	HandleTimeout(tx GenSagaTransaction, timeout int, state GenSagaState) error
	HandleInterim(tx GenSagaTransaction, interim interface{}, state GenSagaState) error

	// HandleGenStageCall this callback is invoked on Process.Call. This method is optional
	// for the implementation
	HandleGenSagaCall(from etf.Tuple, message etf.Term, state GenSagaState) (string, etf.Term)
	// HandleGenStageCast this callback is invoked on Process.Cast. This method is optional
	// for the implementation
	HandleGenSageCast(message etf.Term, state GenSagaState) string
	// HandleGenStageInfo this callback is invoked on Process.Send. This method is optional
	// for the implementation
	HandleGenSagaInfo(message etf.Term, state GenSagaState) string
}

// API

func GenSagaTransactionStart(process *Process, args ...interface{}) (interface{}, error) {
	return nil, nil
}

// default GenSaga callbacks

func (gs *GenSaga) InitSaga(process *Process, args ...interface{}) (GenSagaOptions, error) {
	opts := GenSagaOptions{}
	return opts, nil
}

func (gs *GenSaga) HandleNext(tx GenSagaTransaction, arg interface{}, state GenSagaState) error {
	fmt.Printf("HandleNext: unhandled message %#v\n", tx)
	return nil
}
func (gs *GenSaga) HandleCanceled(tx GenSagaTransaction, reason string, state GenSagaState) error {
	// default callback if it wasn't implemented
	return nil
}
func (gs *GenSaga) HandleInterim(tx GenSagaTransaction, interim interface{}, state GenSagaState) error {
	// default callback if it wasn't implemented
	fmt.Printf("HandleInterim: unhandled message %#v\n", tx)
	return nil
}

func (gs *GenSaga) HandleGenSagaCall(from etf.Tuple, message etf.Term, state GenSagaState) (string, etf.Term) {
	// default callback if it wasn't implemented
	fmt.Printf("HandleGenSagaCall: unhandled message (from %#v) %#v\n", from, message)
	return "reply", etf.Atom("ok")
}

func (gs *GenSaga) HandleGenSagaCast(message etf.Term, state GenSagaState) string {
	// default callback if it wasn't implemented
	fmt.Printf("HandleGenSagaCast: unhandled message %#v\n", message)
	return "noreply"
}
func (gs *GenSaga) HandleGenSagaInfo(message etf.Term, state GenSagaState) string {
	// default callback if it wasn't implemnted
	fmt.Printf("HandleGenSagaInfo: unhandled message %#v\n", message)
	return "noreply"
}

//
// GenServer callbacks
//
func (gs *GenSaga) Init(p *Process, args ...interface{}) (interface{}, error) {
	state := &GenSagaState{}

	state.options, state.internal = p.GetObject().(GenSagaBehaviour).InitSaga(p, args)
	return state, nil
}

func (gs *GenSaga) HandleCall(from etf.Tuple, message etf.Term, state GenServerState) (string, etf.Term) {
	return "reply", "ok"
}

func (gs *GenSaga) HandleCast(message etf.Term, state GenServerState) string {
	return "noreply"
}

func (gs *GenSaga) HandleInfo(message etf.Term, state GenServerState) string {
	return "noreply"
}