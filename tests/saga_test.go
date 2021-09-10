package test

import (
	"fmt"
	"testing"

	"github.com/halturin/ergo"
	"github.com/halturin/ergo/etf"
	"github.com/halturin/ergo/gen"
	"github.com/halturin/ergo/node"
)

type testSaga struct {
	gen.Saga
}

type testSagaWorker struct {
	gen.SagaWorker
}

func (w *testSagaWorker) HandleStartJob(process *gen.SagaWorkerProcess, job gen.SagaJob) error {
	return nil
}
func (w *testSagaWorker) HandleCancelJob(process *gen.SagaWorkerProcess) {
	return
}
func (w *testSagaWorker) HandleWorkerInfo(process *gen.SagaWorkerProcess, message etf.Term) string {
	return "result"
}

func (gs *testSaga) InitSaga(process *gen.SagaProcess, args ...etf.Term) (gen.SagaOptions, error) {
	opts := gen.SagaOptions{
		Worker: &testSagaWorker{},
	}
	return opts, nil
}

func (gs *testSaga) HandleTxNew(process *gen.SagaProcess, tx gen.SagaTransaction, value interface{}) error {
	return nil
}

func (gs *testSaga) HandleTxCancel(process *gen.SagaProcess, tx gen.SagaTransaction, reason string) error {
	return nil
}

func (gs *testSaga) HandleTxResult(process *gen.SagaProcess, tx gen.SagaTransaction, from gen.SagaNext, result interface{}) error {
	return nil
}

func (gs *testSaga) HandleTxInterim(process *gen.SagaProcess, tx gen.SagaTransaction, from gen.SagaNext, interim interface{}) error {

	return nil
}

func (gs *testSaga) HandleTxTimeout(process *gen.SagaProcess, tx gen.SagaTransaction, from gen.SagaNext) error {

	return nil
}

func TestSagaSimple(t *testing.T) {
	fmt.Printf("\n=== Test GenSagaSimple\n")
	fmt.Printf("Starting node: nodeGenSagaSimple01@localhost...")

	node, _ := ergo.StartNode("nodeGenSagaSimple01@localhost", "cookies", node.Options{})

	if node == nil {
		t.Fatal("can't start node")
		return
	}
	fmt.Println("OK")

	fmt.Printf("... starting Saga processes: ")
	saga := &testSaga{}
	saga_process, err := node.Spawn("saga", gen.ProcessOptions{}, saga)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("OK", saga_process.Self())

	node.Stop()
}
