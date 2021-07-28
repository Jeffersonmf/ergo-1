package ergo

type GenSagaWorkerSup struct {
	Supervisor
}

type GenSagaWorkerSupOptions struct {
	Worker GenSagaWorkerBehavior
}

func (ws *GenSagaWorkerSup) Init(args ...interface{}) SupervisorSpec {
	options := args[0].(GenSagaWorkerSupOptions)
	return SupervisorSpec{
		Name: "gen_saga_worker_sup",
		Children: []SupervisorChildSpec{
			SupervisorChildSpec{
				Name:    "gen_saga_worker",
				Child:   options.Worker,
				Restart: SupervisorChildRestartTemporary,
			},
		},
		Strategy: SupervisorStrategy{
			Type:      SupervisorStrategySimpleOneForOne,
			Intensity: 5,
			Period:    5,
		},
	}
}