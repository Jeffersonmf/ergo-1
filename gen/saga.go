package gen

import (
	"fmt"
	"time"

	"github.com/halturin/ergo/etf"
	"github.com/halturin/ergo/lib"
)

type Saga struct {
	Server
}

type SagaTransactionOptions struct {
	// Name defines the name of this transaction. By default
	// this name has autogenerated ID.
	Name string
	// HopLimit defines a number of hop within the transaction. Default limit
	// is 0 (no limit).
	HopLimit uint
	// Lifespan defines a lifespan for the transaction in seconds. Default 0 (no limit)
	Lifespan uint

	// TwoPhaseCommit enables 2PC for the transaction. This option makes all
	// Sagas involved in this transaction invoke HandleCommit on them and
	// invoke HandleCommitJob callback on Worker processes once the transaction is finished.
	TwoPhaseCommit bool
}

type SagaOptions struct {
	// MaxTransactions defines the limit for the number of active transactions. Default: 0 (unlimited)
	MaxTransactions uint
	// Worker
	Worker SagaWorkerBehavior
}

type SagaProcess struct {
	ServerProcess
	Options SagaOptions
	txs     map[string]SagaTransaction

	// simple one for one supervisor for workers
	sv  *SagaWorkerSup
	svp Process
}

type SagaTransaction struct {
	ID        string
	StartTime int64
	Options   SagaTransactionOptions
	Ref       etf.Ref
	Parents   []etf.Pid
	timer     time.Timer
	next      map[string]SagaNext
}

type SagaNext struct {
	// Next - etf.Pid, string (for the locally registered process), etf.Tuple{process, node} (for the remote process)
	Next interface{}
	// Value - a value for the invoking HandleNext on a Next hop.
	Value interface{}
	// Timeout - how long this Saga will be waiting for the result from the Next hop. Default - 10 seconds
	Timeout uint
}

type sagaMessage struct {
	Request string
	Pid     etf.Pid
	Command interface{}
}

type sagaMessageNext struct {
	Transaction SagaTransaction
	Value       interface{}
}

type sagaMessageResult struct {
	Transaction SagaTransaction
	From        etf.Pid
	Result      interface{}
}

type sagaMessageCancel struct {
	Ref    etf.Ref
	ID     string
	Reason string
}
type sagaSetMaxTransactions struct {
	max uint
}

// SagaBehavior interface
type SagaBehavior interface {
	//
	// Mandatory callbacks
	//

	// InitSaga
	InitSaga(state *SagaProcess, args ...etf.Term) error

	// HandleNext
	HandleNext(state *SagaProcess, tx SagaTransaction, value interface{}) error

	// HandleCancel invoked on a request of transaction cancelation.
	HandleCancel(state *SagaProcess, tx SagaTransaction, reason string) error

	// HandleResult
	HandleResult(state *SagaProcess, tx SagaTransaction, next SagaNext, result interface{}) error

	// HandleTimeout
	HandleTimeout(state *SagaProcess, tx SagaTransaction, next SagaNext) error

	//
	// Optional callbacks
	//

	// HandleInterim invoked if received interim result from the Next hop
	HandleInterim(state *SagaProcess, tx SagaTransaction, next SagaNext, interim interface{}) error

	// HandleStageCall this callback is invoked on Process.Call. This method is optional
	// for the implementation
	HandleSagaCall(state *SagaProcess, from ServerFrom, message etf.Term) (string, etf.Term)
	// HandleStageCast this callback is invoked on Process.Cast. This method is optional
	// for the implementation
	HandleSagaCast(state *SagaProcess, message etf.Term) string
	// HandleStageInfo this callback is invoked on Process.Send. This method is optional
	// for the implementation
	HandleSagaInfo(state *SagaProcess, message etf.Term) string

	// HandleJobResult
	HandleJobResult(state *SagaProcess, ref etf.Ref, result interface{}) error
	// HandleJobInterim
	HandleJobInterim(state *SagaProcess, ref etf.Ref, interim interface{}) error
	// HandleJobFailed
	HandleJobFailed(state *SagaProcess, ref etf.Ref) error
}

// SetMaxTransactions set maximum transactions fo the saga
func (gs *Saga) SetMaxTransactions(process *Process, max uint) error {
	message := sagaSetMaxTransactions{
		max: max,
	}
	_, err := process.Direct(message)
	return err
}

func StartSagaTransaction(process *Process, options SagaTransactionOptions, next SagaNext) string {
	if options.Name == "" {
		options.Name = lib.RandomString(32)
	}

	ref := process.MonitorProcess(next.Next)
	tx := SagaTransaction{
		ID:        options.Name,
		Options:   options,
		StartTime: time.Now().Unix(),
		Ref:       ref,
	}

	message := etf.Tuple{
		etf.Atom("$saga_next"),
		process.Self(),
		etf.Tuple{tx, next.Value},
	}

	process.Send(next.Next, message)
	return tx.ID
}

//
// Server callbacks
//
func (gs *Saga) Init(process *ServerProcess, args ...etf.Term) error {
	var options SagaOptions
	if opts, ok := args[0].(SagaOptions); ok {
		options = opts
	}
	sagaProcess := &SagaProcess{
		ServerProcess: *process,
		txs:           make(map[string]SagaTransaction),
	}
	if err := process.Behavior().(SagaBehavior).InitSaga(sagaProcess, args...); err != nil {
		return err
	}
	process.State = sagaProcess

	if options.Worker == nil {
		// do not start supervisor if Worker hasn't been defined
		return nil
	}

	// start supervisor
	svopts := SagaWorkerSupOptions{
		Worker: options.Worker,
	}
	sv := &SagaWorkerSup{}
	svp, err := process.Spawn("gen_saga_worker_sup", gen.ProcessOptions{}, sv, svopts)
	if err != nil {
		return err
	}
	// link saga with supervisor process
	process.Link(svp.Self())

	sagaState.sv = sv
	sagaState.svp = svp

	return nil
}

func (gs *Saga) HandleCall(process *ServerProcess, from ServerFrom, message etf.Term) (string, etf.Term) {
	sp := process.State.(*SagaProcess)
	return process.Behavior().(SagaBehavior).HandleSagaCall(sp, from, message)
}

func (gs *Saga) HandleDirect(process *ServerProcess, message interface{}) (interface{}, error) {
	st := process.State.(*SagaProcess)
	switch m := message.(type) {
	case sagaSetMaxTransactions:
		st.Options.MaxTransactions = m.max
		return nil, nil
	default:
		return nil, ErrUnsupportedRequest
	}
}

func (gs *Saga) HandleCast(process *ServerProcess, message etf.Term) string {
	st := process.State.(*SagaProcess)
	switch m := message.(type) {
	case messageSagaWorkerJobResult:
		process.Behavior().(SagaBehavior).HandleJobResult(st, m.ref, m.result)
		return "noreply"
	case messageSagaWorkerJobInterim:
		process.Behavior().(SagaBehavior).HandleJobInterim(st, m.ref, m.interim)
		return "noreply"
	default:
		return process.Behavior().(SagaBehavior).HandleSagaCast(st, message)
	}
}

func (gs *Saga) HandleInfo(process *ServerProcess, message etf.Term) string {
	var m sagaMessage

	st := process.State.(*SagaProcess)
	// check if we got a MessageDown
	if isDown, d := IsDownMessage(message); isDown {
		if err := handleSagaDown(st, d); err != nil {
			return err.Error()
		}
		return "noreply"
	}

	if err := etf.TermIntoStruct(message, &m); err != nil {
		reply := process.Behavior().(SagaBehavior).HandleSagaInfo(st, message)
		return reply
	}

	err := handleSagaRequest(st, m)
	switch err {
	case nil:
		return "noreply"
	case ErrStop:
		return "stop"
	case ErrUnsupportedRequest:
		reply := process.Behavior().(SagaBehavior).HandleSagaInfo(st, message)
		return reply
	default:
		return err.Error()
	}
}

func handleSagaRequest(state *SagaProcess, m sagaMessage) error {
	var nextMessage sagaMessageNext
	var cancel sagaMessageCancel
	var result sagaMessageResult

	next := SagaNext{}
	switch m.Request {
	case "$saga_next":
		if err := etf.TermIntoStruct(m.Command, &nextMessage); err != nil {
			return ErrUnsupportedRequest
		}

		// Check for the loop
		if _, ok := state.txs[nextMessage.Transaction.ID]; ok {
			cancel := etf.Tuple{
				etf.Atom("$saga_cancel"),
				state.Process.Self(),
				etf.Tuple{nextMessage.Transaction.Ref, nextMessage.Transaction.ID, "loop_detected"},
			}
			state.Process.Send(m.Pid, cancel)
			return nil
		}

		// Check if exceed the number of transaction on this saga
		if len(state.txs)+1 > int(state.Options.MaxTransactions) {
			cancel := etf.Tuple{
				etf.Atom("$saga_cancel"),
				state.Process.Self(),
				etf.Tuple{nextMessage.Transaction.Ref, nextMessage.Transaction.ID, "exceed_max_limit"},
			}
			state.Process.Send(m.Pid, cancel)
			return nil
		}

		// Check if exceed hop limit
		hop := len(nextMessage.Transaction.Parents)
		hoplimit := nextMessage.Transaction.Options.HopLimit
		if hoplimit > 0 && hop+1 > int(hoplimit) {
			cancel := etf.Tuple{
				etf.Atom("$saga_cancel"),
				state.Process.Self(),
				etf.Tuple{nextMessage.Transaction.Ref, nextMessage.Transaction.ID, "exceed_hop_limit"},
			}
			state.Process.Send(m.Pid, cancel)
			return nil
		}

		// Check if lifespan is limited and transaction is too long
		lifespan := nextMessage.Transaction.Options.Lifespan
		l := time.Now().Unix() - nextMessage.Transaction.StartTime
		if lifespan > 0 && l > int64(lifespan) {
			cancel := etf.Tuple{
				etf.Atom("$saga_cancel"),
				state.Process.Self(),
				etf.Tuple{nextMessage.Transaction.Ref, nextMessage.Transaction.ID, "exceed_lifespan"},
			}
			state.Process.Send(m.Pid, cancel)
			return nil
		}

		// everything looks good. go further
		state.txs[nextMessage.Transaction.ID] = nextMessage.Transaction

		//code, value := state.Process.Behavior().(SagaBehavior).HandleNext(state, nextMessage.Transaction, next.Value)
		//switch code {
		//case "result":
		//case "interim":
		//case "next":
		//case "wait":

		//case "cancel":
		//	cancel := etf.Tuple{
		//		etf.Atom("$saga_cancel"),
		//		nextMessage.Transaction.Ref,
		//		nextMessage.Transaction.ID,
		//		value,
		//	}
		//	state.Process.Send(nextMessage.Pid, cancel)
		//	return nil
		//case "stop":
		//	return ErrStop
		//default:
		//	return fmt.Errorf(code)
		//}
		return nil
	case "$saga_cancel":
		if err := etf.TermIntoStruct(m.Command, &cancel); err != nil {
			return ErrUnsupportedRequest
		}
		tx, exist := state.txs[cancel.ID]
		if !exist {
			return nil
		}

		state.Process.Behavior().(SagaBehavior).HandleCancel(state, tx, cancel.Reason)
		return nil
	case "$saga_interim":
		if err := etf.TermIntoStruct(m.Command, &result); err != nil {
			return ErrUnsupportedRequest
		}
		state.Process.Behavior().(SagaBehavior).HandleInterim(state, result.Transaction, next, result.Result)
		return nil
	case "$saga_result":
		if err := etf.TermIntoStruct(m.Command, &result); err != nil {
			return ErrUnsupportedRequest
		}
		state.Process.Behavior().(SagaBehavior).HandleResult(state, result.Transaction, next, result.Result)
		return nil
	}
	return ErrUnsupportedRequest
}

func handleSagaDown(state *SagaProcess, down DownMessage) error {
	return nil
}

//
// default Saga callbacks
//
func (gs *Saga) HandleCommit(state *SagaProcess, tx SagaTransaction) {
	return
}
func (gs *Saga) HandleInterim(state *SagaProcess, tx SagaTransaction, interim interface{}) error {
	// default callback if it wasn't implemented
	fmt.Printf("HandleInterim: unhandled message %#v\n", tx)
	return nil
}
func (gs *Saga) HandleSagaCall(state *SagaProcess, from ServerFrom, message etf.Term) (string, etf.Term) {
	// default callback if it wasn't implemented
	fmt.Printf("HandleSagaCall: unhandled message (from %#v) %#v\n", from, message)
	return "reply", etf.Atom("ok")
}
func (gs *Saga) HandleSagaCast(state *SagaProcess, message etf.Term) string {
	// default callback if it wasn't implemented
	fmt.Printf("HandleSagaCast: unhandled message %#v\n", message)
	return "noreply"
}
func (gs *Saga) HandleSagaInfo(state *SagaProcess, message etf.Term) string {
	// default callback if it wasn't implemnted
	fmt.Printf("HandleSagaInfo: unhandled message %#v\n", message)
	return "noreply"
}
func (gs *Saga) HandleJobResult(state *SagaProcess, ref etf.Ref, result interface{}) error {
	fmt.Printf("HandleJobResult: unhandled message %#v\n", result)
	return nil
}
func (gs *Saga) HandleJobInterim(state *SagaProcess, ref etf.Ref, interim interface{}) error {
	fmt.Printf("HandleJobInterim: unhandled message %#v\n", interim)
	return nil
}
func (gs *Saga) HandleJobFailed(state *SagaProcess, ref etf.Ref) error {
	fmt.Printf("HandleJobFailed: unhandled message %#v\n", ref)
	return nil
}
