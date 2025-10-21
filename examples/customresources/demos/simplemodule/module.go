package main

import (
	"context"
	"sync"
	"time"

	"go.viam.com/rdk/components/servo"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"
)

var myModel = resource.NewModel("acme", "demo", "myservo")

func main() {
	resource.RegisterComponent(servo.API, myModel, resource.Registration[servo.Servo, *myServoConfig]{
		Constructor: newServo,
	})

	module.ModularMain(resource.APIModel{servo.API, myModel})
}

func newServo(ctx context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (servo.Servo, error) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	msc, err := resource.NativeConfig[*myServoConfig](conf)
	if err != nil {
		return nil, err
	}

	s, err := deps.Lookup(servo.Named(msc.RDKServo))
	if err != nil {
		return nil, err
	}

	wg.Go(func() {
		// Wait for 5s before starting a Move call to let all other operations drain out of
		// the manager (and avoid other logic holding onto operation references and stopping
		// GC).
		time.Sleep(5 * time.Second)

		// I use println to avoid putting Log operations in the manager for module -> RDK
		// requests.
		println("BENJI Beginning Move call from module (pay attention now)")
		if err := s.(servo.Servo).Move(ctx, 1, nil); err != nil {
			println("BENJI", err.Error())
		}
	})

	return &myServo{
		Named:  conf.ResourceName().AsNamed(),
		cancel: cancel,
		wg:     &wg,
	}, nil
}

type myServoConfig struct {
	RDKServo string `json:"rdk_servo"`
}

func (msc *myServoConfig) Validate(_ string) ([]string, []string, error) {
	return nil, []string{msc.RDKServo}, nil
}

// myServo is the representation of this model.
type myServo struct {
	resource.Named
	resource.TriviallyReconfigurable
	wg     *sync.WaitGroup
	cancel context.CancelFunc
}

func (ms *myServo) Move(ctx context.Context, angleDeg uint32, extra map[string]interface{}) error {
	return nil
}

func (ms *myServo) Position(ctx context.Context, extra map[string]interface{}) (uint32, error) {
	return 0, nil
}

func (ms *myServo) IsMoving(context.Context) (bool, error) {
	return false, nil
}

func (ms *myServo) Stop(context.Context, map[string]interface{}) error {
	return nil
}

func (ms *myServo) Close(ctx context.Context) error {
	ms.cancel()
	ms.wg.Wait()
	return nil
}
