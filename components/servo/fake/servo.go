// Package fake implements a fake servo.
package fake

import (
	"context"
	"sync/atomic"
	"time"

	"go.viam.com/rdk/components/servo"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

func init() {
	resource.RegisterComponent(
		servo.API,
		resource.DefaultModelFamily.WithModel("fake"),
		resource.Registration[servo.Servo, resource.NoNativeConfig]{
			Constructor: func(
				ctx context.Context, _ resource.Dependencies, conf resource.Config, logger logging.Logger,
			) (servo.Servo, error) {
				return &Servo{
					Named:  conf.ResourceName().AsNamed(),
					logger: logger,
				}, nil
			},
		})
}

// A Servo allows setting and reading a single angle.
type Servo struct {
	angle uint32
	resource.Named
	resource.TriviallyReconfigurable
	resource.TriviallyCloseable
	logger logging.Logger
}

var moveCounter atomic.Int32

// Move sets the given angle.
func (s *Servo) Move(ctx context.Context, angleDeg uint32, extra map[string]interface{}) error {
	// Two modular servos will depend on the fake servo. I've altered the code here to sleep
	// for one second on the first Move call and let all further calls continue. This sleep
	// + the one in opid.go#HasLabel() are meant to create the following interleaving:
	//
	// 1. Move 1 comes in and begins
	// 2. Operation 1 is created and added to the ops map
	// 3. Move 2 comes in and begins
	// 4. HasLabel on Operation 1 is called to see if it's trying to move the same thing as
	//    Move 2.
	// 5. Move 1 finishes
	// 6. Operation 1 is removed from the ops map
	// 7. Operation 1 is left w/o references (the receiver pointer of HasLabel does not
	//    count), and garbage-collection is run
	// 8. Accessing the labels field of Operation 1 in HasLabel causes an NPE

	moveCount := moveCounter.Add(1)
	if moveCount == 1 {
		s.logger.Info("BENJI First Move received; sleeping for a second")
		time.Sleep(time.Second)
		s.logger.Info("BENJI First Move completed; returning")
	} else {
		s.logger.Infof("BENJI Move %d received; immediately returning", moveCount)
	}

	s.angle = angleDeg
	return nil
}

// Position returns the set angle.
func (s *Servo) Position(ctx context.Context, extra map[string]interface{}) (uint32, error) {
	return s.angle, nil
}

// Stop doesn't do anything for a fake servo.
func (s *Servo) Stop(ctx context.Context, extra map[string]interface{}) error {
	return nil
}

// IsMoving is always false for a fake servo.
func (s *Servo) IsMoving(ctx context.Context) (bool, error) {
	return false, nil
}
