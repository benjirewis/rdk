// Package operation manages operation ids
package operation

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/session"
)

type opidKeyType string

const opidKey = opidKeyType("opid")

var methodPrefixesToFilter = [...]string{
	"/proto.rpc.webrtc.v1.SignalingService",
	"/viam.robot.v1.RobotService/StreamStatus",
	"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
}

// Operation is an operation happening on the server.
type Operation struct {
	ID        uuid.UUID
	SessionID uuid.UUID
	Method    string
	Arguments interface{}
	Started   time.Time

	myManager *Manager
	cancel    context.CancelFunc
	labels    []string
}

// Cancel cancel the context associated with an operation.
func (o *Operation) Cancel() {
	o.cancel()
}

// HasLabel returns true if this operation has a specific label.
func (o *Operation) HasLabel(label string) bool {
	// HasLabel should be called when a Move operation is trying to check if other
	// operations are trying to actuate the same component. I saw in Steve's logs that o was
	// non-nil at the beginning of the method, but o.labels _still_ caused an NPE on L71
	// below. My theory of the interleaving that caused that is:
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
	//
	// The sleep on L71 before and the one in fake/servo.go#Move() are meant to create that
	// interleaving.

	method := o.Method
	id := o.ID.String()
	o.myManager.logger.Infow("BENJI HasLabel is running; sleeping for 2 seconds", "method", method, "id", id)
	time.Sleep(2 * time.Second)
	o.myManager.logger.Infow("BENJI HasLabel continuing to o.labels (potential NPE)", "method", method, "id", id)
	for _, l := range o.labels {
		if l == label {
			return true
		}
	}
	return false
}

// CancelOtherWithLabel will cancel all operations besides this one with this label.
func (o *Operation) CancelOtherWithLabel(label string) {
	all := o.myManager.All()
	for _, op := range all {
		if op == o {
			continue
		}
		if op.HasLabel(label) {
			op.Cancel()
		}
	}

	o.labels = append(o.labels, label)
}

func (o *Operation) cleanup() {
	o.myManager.remove(o.ID)
}

// NewManager creates a new manager for holding Operations.
func NewManager(logger logging.Logger) *Manager {
	opLogger := logger.Sublogger("operation_manager")
	return &Manager{ops: map[string]*Operation{}, logger: opLogger}
}

// Manager holds Operations.
type Manager struct {
	ops    map[string]*Operation
	lock   sync.Mutex
	logger logging.Logger
}

func (m *Manager) remove(id uuid.UUID) {
	m.lock.Lock()
	defer m.lock.Unlock()

	op := m.ops[id.String()]
	m.logger.Infow("BENJI deleting an operation from the map", "method", op.Method, "id", id.String())

	delete(m.ops, id.String())

	// Force a GC run with the idea that the delete above may cause the deleted operation to
	// be without references and be garbage-collected (set to nil).
	runtime.GC()
}

func (m *Manager) add(op *Operation) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.logger.Infow("BENJI adding an operation to the map", "method", op.Method, "id", op.ID.String())
	m.ops[op.ID.String()] = op
}

// All returns all running operations.
func (m *Manager) All() []*Operation {
	m.lock.Lock()
	defer m.lock.Unlock()
	a := make([]*Operation, 0, len(m.ops))
	for _, o := range m.ops {
		a = append(a, o)
	}
	return a
}

// Find an Operation.
func (m *Manager) Find(id uuid.UUID) *Operation {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.ops[id.String()]
}

// FindString an Operation.
func (m *Manager) FindString(id string) *Operation {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.ops[id]
}

// Create puts an operation on this context.
func (m *Manager) Create(ctx context.Context, method string, args interface{}) (context.Context, func()) {
	return m.createWithID(ctx, uuid.New(), method, args)
}

func (m *Manager) createWithID(ctx context.Context, id uuid.UUID, method string, args interface{}) (context.Context, func()) {
	if ctx.Value(opidKey) != nil {
		panic("operations cannot be nested")
	}

	for _, val := range methodPrefixesToFilter {
		if strings.HasPrefix(method, val) {
			return ctx, func() {}
		}
	}

	o := m.Find(id)
	if o != nil {
		// Given the exceedingly low chance of a randomly generated UUID colliding, unless it was purposeful, the only way
		// opids will collide is if an operation goes to a module and then back to a dependency as a child operation
		// (e.g. SetPower on a modular base calling into SetPower on a builtin motor). In those cases, we would want to
		// keep track of the original operation, not the incoming one.
		// The parent operation is attached to the context before returning so that the behavior mimicks what happens for operations
		// that stay within the robot.
		m.logger.CDebugw(
			ctx,
			"attempt to create duplicate operation, can ignore if caused by a modular resource calling to a dependency",
			"id",
			id.String(),
			"method",
			method,
		)
		ctx = context.WithValue(ctx, opidKey, o)
		return ctx, func() {}
	}

	op := &Operation{
		ID:        id,
		Method:    method,
		Arguments: args,
		Started:   time.Now(),
		myManager: m,
	}
	if sess, ok := session.FromContext(ctx); ok {
		op.SessionID = sess.ID()
	}
	ctx = context.WithValue(ctx, opidKey, op)
	ctx, op.cancel = context.WithCancel(ctx)
	m.add(op)

	return ctx, func() { op.cleanup() }
}

// Get returns the current Operation. This can be nil.
func Get(ctx context.Context) *Operation {
	o := ctx.Value(opidKey)
	if o == nil {
		return nil
	}
	return o.(*Operation)
}

// CancelOtherWithLabel will cancel all operations besides this one with this label.
// if no Operation is set, will do nothing.
func CancelOtherWithLabel(ctx context.Context, label string) {
	if o := Get(ctx); o != nil {
		o.CancelOtherWithLabel(label)
	}
}
