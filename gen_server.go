package ergo

import (
	"fmt"
	"runtime"
	"sync"

	"github.com/halturin/ergo/etf"
	"github.com/halturin/ergo/lib"
)

const (
	DefaultCallTimeout = 5
)

// GenServerBehaviour interface
type GenServerBehaviour interface {
	// Init(...) -> state
	Init(process *Process, args ...interface{}) (state interface{})

	// HandleCast -> ("noreply", state) - noreply
	//		         ("stop", reason) - stop with reason
	HandleCast(message etf.Term, state interface{}) (string, interface{})

	// HandleCall -> ("reply", message, state) - reply
	//				 ("noreply", _, state) - noreply
	//		         ("stop", reason, _) - normal stop
	HandleCall(from etf.Tuple, message etf.Term, state interface{}) (string, etf.Term, interface{})

	// HandleInfo -> ("noreply", state) - noreply
	//		         ("stop", reason) - normal stop
	HandleInfo(message etf.Term, state interface{}) (string, interface{})

	Terminate(reason string, state interface{})
}

// GenServer is implementation of ProcessBehaviour interface for GenServer objects
type GenServer struct{}

func (gs *GenServer) Loop(p *Process, args ...interface{}) string {
	lockState := &sync.Mutex{}
	object := p.object
	p.state = object.(GenServerBehaviour).Init(p, args...)
	p.ready <- nil

	stop := make(chan string, 2)

	p.currentFunction = "GenServer:loop"

	for {
		var message etf.Term
		var fromPid etf.Pid

		select {
		case ex := <-p.gracefulExit:
			object.(GenServerBehaviour).Terminate(ex.reason, p.state)
			return ex.reason

		case reason := <-stop:
			object.(GenServerBehaviour).Terminate(reason, p.state)
			return reason

		case msg := <-p.mailBox:
			fromPid = msg.Element(1).(etf.Pid)
			message = msg.Element(2)

		case <-p.Context.Done():
			return "kill"

		case direct := <-p.direct:
			gs.handleDirect(direct)
			continue
		}

		lib.Log("[%s]. %v got message from %#v\n", p.Node.FullName, p.self, fromPid)

		p.reductions++

		panicHandler := func() {
			if r := recover(); r != nil {
				pc, fn, line, _ := runtime.Caller(2)
				fmt.Printf("Warning: GenServer recovered (name: %s) %v %#v at %s[%s:%d]\n",
					p.Name(), p.self, r, runtime.FuncForPC(pc).Name(), fn, line)
				stop <- "panic"
			}
		}

		switch m := message.(type) {
		case etf.Tuple:
			switch mtag := m.Element(1).(type) {
			case etf.Atom:
				switch mtag {
				case etf.Atom("$gen_call"):
					// We need to wrap it out using goroutine in order to serve
					// sync-requests (like 'process.Call') within callback execution
					// since reply (etf.Ref) comes through the same mailBox channel
					go func() {
						defer panicHandler()

						lockState.Lock()
						defer lockState.Unlock()

						fromTuple := m.Element(2).(etf.Tuple)

						cf := p.currentFunction
						p.currentFunction = "GenServer:HandleCall"
						code, reply, state := object.(GenServerBehaviour).HandleCall(fromTuple, m.Element(3), p.state)
						p.currentFunction = cf

						if code == "stop" {
							stop <- reply.(string)
							// do not unlock, coz we have to keep this state unchanged for Terminate handler
							return
						}

						p.state = state

						if reply != nil && code == "reply" {
							pid := fromTuple.Element(1).(etf.Pid)
							ref := fromTuple.Element(2)
							rep := etf.Term(etf.Tuple{ref, reply})
							p.Send(pid, rep)
						}
					}()

				case etf.Atom("$gen_cast"):
					go func() {
						defer panicHandler()

						lockState.Lock()
						defer lockState.Unlock()

						cf := p.currentFunction
						p.currentFunction = "GenServer:HandleCast"
						code, state := object.(GenServerBehaviour).HandleCast(m.Element(2), p.state)
						p.currentFunction = cf

						if code == "stop" {
							stop <- state.(string)
							return
						}
						p.state = state
					}()

				default:
					go func() {
						defer panicHandler()

						lockState.Lock()
						defer lockState.Unlock()

						cf := p.currentFunction
						p.currentFunction = "GenServer:HandleInfo"
						code, state := object.(GenServerBehaviour).HandleInfo(message, p.state)
						p.currentFunction = cf

						if code == "stop" {
							stop <- state.(string)
							return
						}
						p.state = state
					}()

				}

			case etf.Ref:
				lib.Log("got reply: %#v\n%#v", mtag, message)
				p.reply <- m

			default:
				lib.Log("mtag: %#v", mtag)
				go func() {
					defer panicHandler()

					lockState.Lock()
					defer lockState.Unlock()

					cf := p.currentFunction
					p.currentFunction = "GenServer:HandleInfo"
					code, state := object.(GenServerBehaviour).HandleInfo(message, p.state)
					p.currentFunction = cf

					if code == "stop" {
						stop <- state.(string)
					}
					p.state = state
				}()
			}

		default:
			lib.Log("m: %#v", m)
			go func() {
				defer panicHandler()

				lockState.Lock()
				defer lockState.Unlock()

				cf := p.currentFunction
				p.currentFunction = "GenServer:HandleInfo"
				code, state := object.(GenServerBehaviour).HandleInfo(message, p.state)
				p.currentFunction = cf

				if code == "stop" {
					stop <- state.(string)
					return
				}
				p.state = state
			}()
		}
	}
}

func (gs *GenServer) handleDirect(m directMessage) {

	if m.reply != nil {
		m.err = ErrUnsupportedRequest
		m.reply <- m
	}
}
