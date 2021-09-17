package gen

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/halturin/ergo/etf"
)

// SagaBehavior interface
type SagaBehavior interface {
	//
	// Mandatory callbacks
	//

	// InitSaga
	InitSaga(process *SagaProcess, args ...etf.Term) (SagaOptions, error)

	// HandleTxNew invokes on a new TX receiving by this saga.
	HandleTxNew(process *SagaProcess, id SagaTransactionID, value interface{}) SagaStatus

	// HandleTxResult invoked on a receiving result from the next saga
	HandleTxResult(process *SagaProcess, id SagaTransactionID, from SagaStepID, result interface{}) SagaStatus

	// HandleTxCancel invoked on a request of transaction cancelation.
	HandleTxCancel(process *SagaProcess, id SagaTransactionID, reason string) SagaStatus

	//
	// Optional callbacks
	//

	// HandleDone invoked when the TX is done. Invoked on a saga where this tx was created.
	HandleTxDone(process *SagaProcess, id SagaTransactionID) SagaStatus

	// HandleInterim invoked if received interim result from the next hop
	HandleTxInterim(process *SagaProcess, id SagaTransactionID, from SagaStepID, interim interface{}) SagaStatus

	//
	// Callbacks to handle result/interim from the worker(s)
	//

	// HandleJobResult
	HandleJobResult(process *SagaProcess, id SagaJobID, result interface{}) SagaStatus
	// HandleJobInterim
	HandleJobInterim(process *SagaProcess, id SagaJobID, interim interface{}) SagaStatus
	// HandleJobFailed
	HandleJobFailed(process *SagaProcess, id SagaJobID, reason string) SagaStatus

	//
	// Server's callbacks
	//

	// HandleStageCall this callback is invoked on Process.Call. This method is optional
	// for the implementation
	HandleSagaCall(process *SagaProcess, from ServerFrom, message etf.Term) (etf.Term, ServerStatus)
	// HandleStageCast this callback is invoked on Process.Cast. This method is optional
	// for the implementation
	HandleSagaCast(process *SagaProcess, message etf.Term) ServerStatus
	// HandleStageInfo this callback is invoked on Process.Send. This method is optional
	// for the implementation
	HandleSagaInfo(process *SagaProcess, message etf.Term) ServerStatus
	// HandleSagaDirect this callback is invoked on Process.Direct. This method is optional
	// for the implementation
	HandleSagaDirect(process *SagaProcess, message interface{}) (interface{}, error)
}

const (
	defaultHopLimit = math.MaxUint16
	defaultLifespan = 60
)

type SagaStatus error

var (
	SagaStatusOK          SagaStatus // nil
	SagaStatusStop        SagaStatus = fmt.Errorf("stop")
	sagaStatusUnsupported SagaStatus = fmt.Errorf("unsupported")
)

type Saga struct {
	Server
}

type SagaTransactionOptions struct {
	// HopLimit defines a number of hop within the transaction. Default limit
	// is 0 (no limit).
	HopLimit uint
	// Lifespan defines a lifespan for the transaction in seconds. Must be > 0 (default is 60).
	Lifespan uint

	// TwoPhaseCommit enables 2PC for the transaction. This option makes all
	// Sagas involved in this transaction invoke HandleCommit callback on them and
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
	options SagaOptions

	// running transactions
	txs      map[SagaTransactionID]*SagaTransaction
	mutexTXS sync.Mutex

	// saga steps where txs were sent or where txs came from
	steps      map[SagaStepID]*SagaTransaction
	mutexSteps sync.Mutex

	// running jobs
	jobs      map[etf.Pid]*SagaJob
	mutexJobs sync.Mutex
}

type SagaTransactionID etf.Ref

func (id SagaTransactionID) String() string {
	r := etf.Ref(id)
	return fmt.Sprintf("TX#%d.%d.%d", r.ID[0], r.ID[1], r.ID[2])
}

type SagaTransaction struct {
	sync.Mutex
	id      SagaTransactionID
	options SagaTransactionOptions
	origin  SagaStepID               // where it came from
	monitor etf.Ref                  // monitor parent process
	steps   map[SagaStepID]*SagaStep // where were sent
	jobs    map[etf.Pid]bool
	arrival int64     // when it arrived on this saga
	parents []etf.Pid // sagas trace
}

type SagaStepID etf.Ref

func (id SagaStepID) String() string {
	r := etf.Ref(id)
	return fmt.Sprintf("Step#%d.%d.%d", r.ID[0], r.ID[1], r.ID[2])
}

type SagaStep struct {
	// Saga - etf.Pid, string (for the locally registered process), gen.ProcessID{process, node} (for the remote process)
	Saga interface{}
	// Value - a value for the invoking HandleTX on a next hop.
	Value interface{}
	// Timeout - how long this Saga will be waiting for the result from the next hop. Default - 10 seconds
	Timeout uint

	// internal
	done bool // for 2PC case
}

type SagaJobID etf.Ref

func (id SagaJobID) String() string {
	r := etf.Ref(id)
	return fmt.Sprintf("Job#%d.%d.%d", r.ID[0], r.ID[1], r.ID[2])
}

type SagaJob struct {
	ID    SagaJobID
	TXID  SagaTransactionID
	Value interface{}

	// internal
	options SagaJobOptions
	saga    etf.Pid
	commit  bool
	worker  Process
	done    bool
}

type SagaJobOptions struct {
	Timeout uint
}

type messageSaga struct {
	Request etf.Atom
	Pid     etf.Pid
	Command interface{}
}

type messageSagaStep struct {
	Origin        etf.Ref
	TransactionID etf.Ref
	Value         interface{}
	Parents       []etf.Pid
	Options       map[string]interface{}
}

type messageSagaResult struct {
	TransactionID etf.Ref
	Origin        etf.Ref
	Result        interface{}
}

type messageSagaCancel struct {
	Transaction etf.Ref
	Origin      etf.Ref
	Reason      string
}

//
// Saga API
//

type sagaSetMaxTransactions struct {
	max uint
}

// SetMaxTransactions set maximum transactions fo the saga
func (gs *Saga) SetMaxTransactions(process Process, max uint) error {
	if !process.IsAlive() {
		return ErrServerTerminated
	}
	message := sagaSetMaxTransactions{
		max: max,
	}
	_, err := process.Direct(message)
	return err
}

//
// SagaProcess methods
//

func (sp *SagaProcess) StartTransaction(name string, options SagaTransactionOptions, value interface{}) SagaTransactionID {
	tx := SagaTransaction{
		id:      SagaTransactionID(sp.MakeRef()),
		arrival: time.Now().Unix(),
		steps:   make(map[SagaStepID]*SagaStep),
		jobs:    make(map[etf.Pid]bool),
	}
	if options.HopLimit == 0 {
		options.HopLimit = defaultHopLimit
	}
	if options.Lifespan == 0 {
		options.Lifespan = defaultLifespan
	}
	tx.options = options

	sp.mutexTXS.Lock()
	sp.txs[tx.id] = &tx
	sp.mutexTXS.Unlock()

	step := SagaStep{
		Saga:  sp.Self(),
		Value: value,
	}
	sp.Next(tx.id, step)

	return tx.id

}

func (sp *SagaProcess) CancelTransaction(id SagaTransactionID, reason string) {
	sp.mutexTXS.Lock()
	tx, ok := sp.txs[id]
	sp.mutexTXS.Unlock()
	if !ok {
		return
	}

	message := etf.Tuple{
		etf.Atom("$saga_cancel"),
		sp.Self(),
		etf.Tuple{tx.id, etf.Ref(tx.origin), reason},
	}
	sp.Send(sp.Self(), message)
}

func (sp *SagaProcess) Next(id SagaTransactionID, step SagaStep) (SagaStepID, error) {
	sp.mutexTXS.Lock()
	tx, ok := sp.txs[id]
	sp.mutexTXS.Unlock()
	if !ok {
		return SagaStepID{}, fmt.Errorf("unknown transaction")
	}

	if len(tx.steps) > int(tx.options.HopLimit) {
		return SagaStepID{}, fmt.Errorf("exceeded hop limit")
	}

	step_Lifespan := int64(tx.options.Lifespan) - (time.Now().Unix() - tx.arrival)
	if step_Lifespan < 1 {
		sp.CancelTransaction(id, "exceeded lifespan")
		return SagaStepID{}, fmt.Errorf("exceeded lifespan. transaction canceled")
	}

	ref := sp.MonitorProcess(step.Saga)
	step_id := SagaStepID(ref)
	message := etf.Tuple{
		etf.Atom("$saga_next"),
		sp.Self(),
		etf.Tuple{
			ref,            // step id
			etf.Ref(tx.id), // tx id
			step.Value,
			tx.parents,
			etf.Map{
				"HopLimit":       tx.options.HopLimit,
				"Lifespan":       step_Lifespan,
				"TwoPhaseCommit": tx.options.TwoPhaseCommit,
			},
		},
	}

	sp.Send(step.Saga, message)

	tx.Lock()
	tx.steps[step_id] = &step
	tx.Unlock()

	sp.mutexSteps.Lock()
	sp.steps[step_id] = tx
	sp.mutexSteps.Unlock()

	// FIXME handle next.Timeout

	return step_id, nil
}

func (sp *SagaProcess) StartJob(id SagaTransactionID, options SagaJobOptions, value interface{}) (SagaJobID, error) {
	var job SagaJob

	if sp.options.Worker == nil {
		return job.ID, fmt.Errorf("This saga has no worker")
	}
	sp.mutexTXS.Lock()
	tx, ok := sp.txs[id]
	sp.mutexTXS.Unlock()

	if !ok {
		return job.ID, fmt.Errorf("unknown transaction")
	}

	// FIXME make context WithTimeout to limit the lifespan
	workerOptions := ProcessOptions{}
	worker, err := sp.Spawn("", workerOptions, sp.options.Worker)
	if err != nil {
		return job.ID, err
	}
	sp.Link(worker.Self())

	job.ID = SagaJobID(sp.MakeRef())
	job.TXID = id
	job.Value = value
	job.commit = tx.options.TwoPhaseCommit
	job.saga = sp.Self()
	job.worker = worker

	m := messageSagaJobStart{
		job: job,
	}
	if err := sp.Cast(worker.Self(), m); err != nil {
		worker.Kill()
		return SagaJobID{}, err
	}

	sp.mutexJobs.Lock()
	sp.jobs[worker.Self()] = &job
	sp.mutexJobs.Unlock()

	tx.Lock()
	tx.jobs[worker.Self()] = true
	tx.Unlock()

	return job.ID, nil
}

func (sp *SagaProcess) CancelJob(job SagaJobID) error {
	return nil

}

func (sp *SagaProcess) SendResult(id SagaTransactionID, result interface{}) error {
	sp.mutexTXS.Lock()
	tx, ok := sp.txs[id]
	sp.mutexTXS.Unlock()
	if !ok {
		return fmt.Errorf("unknown transaction")
	}

	if len(tx.parents) == 0 {
		return fmt.Errorf("no parent saga")
	}

	message := etf.Tuple{
		etf.Atom("$saga_result"),
		sp.Self(),
		etf.Tuple{
			etf.Ref(tx.id),
			etf.Ref(tx.origin),
			result,
		},
	}
	if tx.options.TwoPhaseCommit == false {

	}

	// send message to the parent saga
	// FIXME handle Call result (value)
	if _, err := sp.Call(tx.parents[0], message); err != nil {
		return err
	}

	// do not remove if 2PC is enabled
	if tx.options.TwoPhaseCommit == false {
		sp.mutexTXS.Lock()
		delete(sp.txs, id)
		sp.mutexTXS.Unlock()
	}

	return nil
}

func (sp *SagaProcess) SendInterim(id SagaTransactionID, interim interface{}) error {
	sp.mutexTXS.Lock()
	tx, ok := sp.txs[id]
	sp.mutexTXS.Unlock()
	if !ok {
		return fmt.Errorf("unknown transaction")
	}

	if len(tx.parents) == 0 {
		return fmt.Errorf("no parent saga")
	}

	message := etf.Tuple{
		etf.Atom("$saga_interim"),
		sp.Self(),
		etf.Tuple{
			etf.Ref(tx.id),
			etf.Ref(tx.origin),
			interim,
		},
	}

	// send message to the parent saga
	// FIXME handle Call result (value)
	if _, err := sp.Call(tx.parents[0], message); err != nil {
		return err
	}

	return nil
}

func (sp *SagaProcess) checkTxDone(tx *SagaTransaction) bool {

	if tx.options.TwoPhaseCommit == false { // 2PC is disabled
		if len(tx.steps) > 0 { // haven't received all results from the "next" sagas
			return false
		}
		if len(tx.jobs) > 0 { // tx has running jobs
			return false
		}
		return true
	}

	// 2PC is enabled. check whether received all results from sagas
	// and workers have finished their jobs

	tx.Lock()
	// check results from sagas
	for _, step := range tx.steps {
		if step.done == false {
			tx.Unlock()
			return false
		}
	}

	if len(tx.jobs) == 0 {
		tx.Unlock()
		return true
	}

	// gen list of running workers
	jobs := []etf.Pid{}
	for pid, _ := range tx.jobs {
		jobs = append(jobs, pid)
	}
	tx.Unlock()

	// check the job states of them
	sp.mutexJobs.Lock()
	for _, pid := range jobs {
		job := sp.jobs[pid]
		if job.done == false {
			sp.mutexJobs.Unlock()
			return false
		}
	}
	sp.mutexJobs.Unlock()
	return true
}

func (sp *SagaProcess) handleSagaRequest(m messageSaga) error {
	var stepMessage messageSagaStep
	var result messageSagaResult
	//var cancel messageSagaCancel

	switch m.Request {
	case etf.Atom("$saga_next"):
		if err := etf.TermIntoStruct(m.Command, &stepMessage); err != nil {
			return ErrUnsupportedRequest
		}

		// Check if exceed the number of transaction on this saga
		if sp.options.MaxTransactions > 0 && len(sp.txs)+1 > int(sp.options.MaxTransactions) {
			cancel := etf.Tuple{
				etf.Atom("$saga_cancel"),
				sp.Self(),
				etf.Tuple{
					stepMessage.Origin,
					stepMessage.TransactionID,
					"exceed_tx_limit",
				},
			}
			sp.Send(m.Pid, cancel)
			return nil
		}

		// Check for the loop
		transactionID := SagaTransactionID(stepMessage.TransactionID)
		sp.mutexTXS.Lock()
		tx, ok := sp.txs[transactionID]
		if !ok {
			// came from remote saga
			txOptions := SagaTransactionOptions{
				HopLimit: defaultHopLimit,
				Lifespan: defaultLifespan,
			}
			if value, ok := stepMessage.Options["HopLimit"]; ok {
				if hoplimit, ok := value.(int64); ok {
					txOptions.HopLimit = uint(hoplimit)
				}
			}
			if value, ok := stepMessage.Options["Lifespan"]; ok {
				if lifespan, ok := value.(int64); ok {
					txOptions.Lifespan = uint(lifespan)
				}
			}
			if value, ok := stepMessage.Options["TwoPhaseCommit"]; ok {
				txOptions.TwoPhaseCommit, _ = value.(bool)
			}

			tx = &SagaTransaction{
				id:      transactionID,
				options: txOptions,
				origin:  SagaStepID(stepMessage.Origin),
				steps:   make(map[SagaStepID]*SagaStep),
				jobs:    make(map[etf.Pid]bool),
				parents: append([]etf.Pid{m.Pid}, stepMessage.Parents...),
			}
			sp.txs[transactionID] = tx

		} else {
			if len(tx.parents) > 0 {
				cancel := etf.Tuple{
					etf.Atom("$saga_cancel"),
					sp.Self(),
					etf.Tuple{
						stepMessage.Origin,
						stepMessage.TransactionID,
						"loop_detected",
					},
				}
				sp.Send(m.Pid, cancel)
				sp.mutexTXS.Unlock()
				return nil

			}
			tx = &SagaTransaction{
				arrival: time.Now().Unix(),
				parents: append([]etf.Pid{m.Pid}, stepMessage.Parents...),
			}
		}
		sp.mutexTXS.Unlock()

		tx.monitor = sp.MonitorProcess(m.Pid)
		return sp.Behavior().(SagaBehavior).HandleTxNew(sp, transactionID, stepMessage.Value)

	//case "$saga_cancel":
	//	if err := etf.TermIntoStruct(m.Command, &cancel); err != nil {
	//		return ErrUnsupportedRequest
	//	}
	//	tx, exist := sp.txs[SagaTransactionID(cancel.ID)]
	//	if !exist {
	//		return nil
	//	}

	//	sp.Behavior().(SagaBehavior).HandleTxCancel(sp, tx.id, cancel.Reason)
	//	return nil
	//case "$saga_interim":
	//	if err := etf.TermIntoStruct(m.Command, &result); err != nil {
	//		return ErrUnsupportedRequest
	//	}
	//	sp.Behavior().(SagaBehavior).HandleTxInterim(sp, result.Transaction.id, next, result.Result)
	//	return nil
	case etf.Atom("$saga_result"):
		if err := etf.TermIntoStruct(m.Command, &result); err != nil {
			return ErrUnsupportedRequest
		}

		step_id := SagaStepID(result.Origin)
		sp.mutexSteps.Lock()
		tx, ok := sp.steps[step_id]
		if !ok {
			sp.mutexSteps.Unlock()
			// ignore unknown result
			return nil
		} else {
			delete(sp.steps, step_id)
			if tx.id != SagaTransactionID(result.TransactionID) {
				sp.mutexSteps.Unlock()
				return fmt.Errorf("transaction id mismatch in saga result")
			}
		}
		sp.mutexSteps.Unlock()

		tx.Lock()
		if tx.options.TwoPhaseCommit == false {
			delete(tx.steps, step_id)
		} else {
			step := tx.steps[step_id]
			step.done = true
		}
		tx.Unlock()

		// FIXME do not call it here. calling saga process is on hold during this call
		// FIXME handle returned status
		sp.Behavior().(SagaBehavior).HandleTxResult(sp, tx.id, step_id, result.Result)

		return nil
	case etf.Atom("$saga_interim"):
		if err := etf.TermIntoStruct(m.Command, &result); err != nil {
			return ErrUnsupportedRequest
		}
		step_id := SagaStepID(result.Origin)
		sp.mutexSteps.Lock()
		tx, ok := sp.steps[step_id]
		if !ok {
			sp.mutexSteps.Unlock()
			// ignore unknown result
			return nil
		}
		sp.mutexSteps.Unlock()
		// FIXME do not call it here. calling saga process is on hold during this call
		// FIXME handle returned status
		sp.Behavior().(SagaBehavior).HandleTxInterim(sp, tx.id, step_id, result.Result)
		return nil
	}
	return sagaStatusUnsupported
}

func (sp *SagaProcess) handleSagaExit(exit MessageExit) error {
	sp.mutexJobs.Lock()
	job, ok := sp.jobs[exit.Pid]
	sp.mutexJobs.Unlock()
	if !ok {
		// must be already handled as finished and was removed from there
		return nil
	}
	if exit.Reason != "normal" {
		return sp.Behavior().(SagaBehavior).HandleJobFailed(sp, job.ID, exit.Reason)
	}

	return sp.Behavior().(SagaBehavior).HandleJobFailed(sp, job.ID, "no result")
}

func (sp *SagaProcess) handleSagaDown(down MessageDown) error {
	return nil
}

//
// Server callbacks
//

func (gs *Saga) Init(process *ServerProcess, args ...etf.Term) error {
	var options SagaOptions

	behavior, ok := process.Behavior().(SagaBehavior)
	if !ok {
		return fmt.Errorf("Saga: not a SagaBehavior")
	}

	sagaProcess := &SagaProcess{
		ServerProcess: *process,
		txs:           make(map[SagaTransactionID]*SagaTransaction),
		steps:         make(map[SagaStepID]*SagaTransaction),
	}
	// do not inherite parent State
	sagaProcess.State = nil

	options, err := behavior.InitSaga(sagaProcess, args...)
	if err != nil {
		return err
	}

	sagaProcess.options = options
	process.State = sagaProcess

	if options.Worker != nil {
		sagaProcess.jobs = make(map[etf.Pid]*SagaJob)
	}

	process.SetTrapExit(true)

	return nil
}

func (gs *Saga) HandleCall(process *ServerProcess, from ServerFrom, message etf.Term) (etf.Term, ServerStatus) {
	var status SagaStatus
	var value etf.Term
	var mSaga messageSaga

	sp := process.State.(*SagaProcess)

	switch m := message.(type) {
	case messageSagaJobResult:
		sp.mutexJobs.Lock()
		job, ok := sp.jobs[m.pid]
		sp.mutexJobs.Unlock()
		if !ok {
			status = SagaStatusOK
			break
		}

		sp.mutexTXS.Lock()
		tx, ok := sp.txs[job.TXID]
		sp.mutexTXS.Unlock()

		if !ok {
			sp.mutexJobs.Lock()
			delete(sp.jobs, m.pid)
			sp.mutexJobs.Unlock()

			// might be already canceled. just ignore it
			status = SagaStatusOK
			break
		}

		if tx.options.TwoPhaseCommit == false {
			sp.mutexJobs.Lock()
			delete(sp.jobs, m.pid)
			sp.mutexJobs.Unlock()

			tx.Lock()
			delete(tx.jobs, m.pid)
			tx.Unlock()
		} else {
			job.done = true
		}

		status = process.Behavior().(SagaBehavior).HandleJobResult(sp, job.ID, m.result)

	case messageSagaJobInterim:
		sp.mutexJobs.Lock()
		job, ok := sp.jobs[m.pid]
		sp.mutexJobs.Unlock()
		if !ok {
			// might be already canceled. just ignore it
			status = SagaStatusOK
			break
		}
		status = process.Behavior().(SagaBehavior).HandleJobInterim(sp, job.ID, m.interim)

	default:
		if err := etf.TermIntoStruct(message, &mSaga); err == nil {
			// handle Interim and Result messages comes from the "next" sagas
			s := sp.handleSagaRequest(mSaga)
			status = ServerStatus(s)
			break
		}

		value, status = process.Behavior().(SagaBehavior).HandleSagaCall(sp, from, message)
	}
	switch status {
	case SagaStatusOK:
		return value, ServerStatusOK
	case SagaStatusStop:
		return value, ServerStatusStop
	default:
		return value, ServerStatus(status)
	}
}

func (gs *Saga) HandleDirect(process *ServerProcess, message interface{}) (interface{}, error) {
	st := process.State.(*SagaProcess)
	switch m := message.(type) {
	case sagaSetMaxTransactions:
		st.options.MaxTransactions = m.max
		return nil, nil
	default:
		return process.Behavior().(SagaBehavior).HandleSagaDirect(st, message)
	}
}

func (gs *Saga) HandleCast(process *ServerProcess, message etf.Term) ServerStatus {
	var status SagaStatus
	sp := process.State.(*SagaProcess)
	status = process.Behavior().(SagaBehavior).HandleSagaCast(sp, message)
	return SagaStatus(status)
}

func (gs *Saga) HandleInfo(process *ServerProcess, message etf.Term) ServerStatus {
	var mSaga messageSaga

	sp := process.State.(*SagaProcess)
	switch m := message.(type) {
	case MessageExit:
		// handle worker exit message
		if err := sp.handleSagaExit(m); err != nil {
			return ServerStatus(err)
		}
		return ServerStatusOK
	case MessageDown:
		// handle saga's down message
		if err := sp.handleSagaDown(m); err != nil {
			return ServerStatus(err)
		}
		return ServerStatusOK
	default:
		if err := etf.TermIntoStruct(message, &mSaga); err != nil {
			status := process.Behavior().(SagaBehavior).HandleSagaInfo(sp, message)
			return status
		}
	}

	status := sp.handleSagaRequest(mSaga)
	switch status {
	case nil:
		return ServerStatusOK
	case SagaStatusStop:
		return ServerStatusStop
	case sagaStatusUnsupported:
		return process.Behavior().(SagaBehavior).HandleSagaInfo(sp, message)
	default:
		return ServerStatus(status)
	}
}

func (gs *Saga) Terminate(process *ServerProcess, reason string) {
	fmt.Println("SAGA terminated")
}

//
// default Saga callbacks
//

func (gs *Saga) HandleTxInterim(process *SagaProcess, tx SagaTransaction, interim interface{}) SagaStatus {
	fmt.Printf("HandleInterim: unhandled message %#v\n", tx)
	return ServerStatusOK
}
func (gs *Saga) HandleSagaCall(process *SagaProcess, from ServerFrom, message etf.Term) (etf.Term, ServerStatus) {
	fmt.Printf("HandleSagaCall: unhandled message (from %#v) %#v\n", from, message)
	return etf.Atom("ok"), ServerStatusOK
}
func (gs *Saga) HandleSagaCast(process *SagaProcess, message etf.Term) ServerStatus {
	fmt.Printf("HandleSagaCast: unhandled message %#v\n", message)
	return ServerStatusOK
}
func (gs *Saga) HandleSagaInfo(process *SagaProcess, message etf.Term) ServerStatus {
	fmt.Printf("HandleSagaInfo: unhandled message %#v\n", message)
	return ServerStatusOK
}
func (gs *Saga) HandleJobResult(process *SagaProcess, id SagaJobID, result interface{}) SagaStatus {
	fmt.Printf("HandleJobResult: unhandled message %#v\n", result)
	return SagaStatusOK
}
func (gs *Saga) HandleJobInterim(process *SagaProcess, id SagaJobID, interim interface{}) SagaStatus {
	fmt.Printf("HandleJobInterim: unhandled message %#v\n", interim)
	return SagaStatusOK
}
func (gs *Saga) HandleJobFailed(process *SagaProcess, id SagaJobID, reason string) SagaStatus {
	fmt.Printf("HandleJobFailed: unhandled message %s. reason %q\n", id, reason)
	return nil
}
