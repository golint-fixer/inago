package task

import (
	"time"

	"github.com/satori/go.uuid"

	"github.com/giantswarm/inago/logging"
)

// Action represents any work to be done when executing a task.
type Action func() error

// Task represents a task that is executable.
type Task struct {
	// ActiveStatus represents a status indicating activation or deactivation.
	ActiveStatus ActiveStatus

	// Error represents the message of an error occured during task execution, if
	// any.
	Error string

	// FinalStatus represents any status that is final. A task having this status
	// will not change its status anymore.
	FinalStatus FinalStatus

	// ID represents the task identifier.
	ID string
}

// Service represents a task managing unit being able to act on task
// objects.
type Service interface {
	// Create creates a new task object configured with the given action. The
	// task object is immediately returned and its corresponding action is
	// executed asynchronously.
	Create(action Action) (*Task, error)

	// FetchState fetches and returns the current state and status for the given
	// task ID.
	FetchState(taskID string) (*Task, error)

	// MarkAsSucceeded marks the task object as succeeded and persists its state.
	// The returned task object is actually the refreshed version of the provided
	// one.
	MarkAsSucceeded(taskObject *Task) (*Task, error)

	// MarkAsFailedWithError marks the task object as failed, adds information of
	// thegiven error and persists the task objects's state. The returned task
	// object is actually the refreshed version of the provided one.
	MarkAsFailedWithError(taskObject *Task, err error) (*Task, error)

	// PersistState writes the given task object to the configured Storage.
	PersistState(taskObject *Task) error

	// WaitForFinalStatus blocks and waits for the given task to reach a final
	// status. The given closer can end the waiting and thus stop blocking the
	// call to WaitForFinalStatus.
	WaitForFinalStatus(taskID string, closer <-chan struct{}) (*Task, error)
}

// Config represents the configurations for the task service that is
// going to be created.
type Config struct {
	Storage Storage

	// WaitSleep represents the time to sleep between state-check cycles.
	WaitSleep time.Duration
}

// DefaultConfig returns a best effort default configuration for the
// task service.
func DefaultConfig() Config {
	newConfig := Config{
		Storage:   NewMemoryStorage(),
		WaitSleep: 1 * time.Second,
	}

	return newConfig
}

// NewTaskService returns a new configured task service instance.
func NewTaskService(config Config) Service {
	newTaskService := &taskService{
		Config: config,
	}

	return newTaskService
}

type taskService struct {
	Config
}

func (ts *taskService) Create(action Action) (*Task, error) {
	logger := logging.GetLogger()

	taskObject := &Task{
		ID:           uuid.NewV4().String(),
		ActiveStatus: StatusStarted,
		FinalStatus:  "",
	}

	go func() {
		err := action()
		if err != nil {
			_, markErr := ts.MarkAsFailedWithError(taskObject, err)
			if markErr != nil {
				logger.Error(nil, "[E] Task.MarkAsFailed failed: %#v", maskAny(markErr))
				return
			}
			return
		}

		_, err = ts.MarkAsSucceeded(taskObject)
		if err != nil {
			logger.Error(nil, "[E] Task.MarkAsSucceeded failed: %#v", maskAny(err))
			return
		}
	}()

	err := ts.PersistState(taskObject)
	if err != nil {
		return nil, maskAny(err)
	}

	return taskObject, nil
}

func (ts *taskService) FetchState(taskID string) (*Task, error) {
	var err error

	taskObject, err := ts.Storage.Get(taskID)
	if err != nil {
		return nil, maskAny(err)
	}

	return taskObject, nil
}

func (ts *taskService) MarkAsFailedWithError(taskObject *Task, err error) (*Task, error) {
	taskObject.ActiveStatus = StatusStopped
	taskObject.Error = err.Error()
	taskObject.FinalStatus = StatusFailed

	err = ts.PersistState(taskObject)
	if err != nil {
		return nil, maskAny(err)
	}

	return taskObject, nil
}

func (ts *taskService) MarkAsSucceeded(taskObject *Task) (*Task, error) {
	taskObject.ActiveStatus = StatusStopped
	taskObject.FinalStatus = StatusSucceeded

	err := ts.PersistState(taskObject)
	if err != nil {
		return nil, maskAny(err)
	}

	return taskObject, nil
}

func (ts *taskService) PersistState(taskObject *Task) error {
	err := ts.Storage.Set(taskObject)
	if err != nil {
		return maskAny(err)
	}

	return nil
}

// WaitForFinalStatus acts as described in the interface comments. Note that
// both, task object and error will be nil in case the closer ends waiting for
// the task to reach a final state.
func (ts *taskService) WaitForFinalStatus(taskID string, closer <-chan struct{}) (*Task, error) {
	for {
		select {
		case <-closer:
			return nil, nil
		case <-time.After(ts.WaitSleep):
			taskObject, err := ts.FetchState(taskID)
			if err != nil {
				return nil, maskAny(err)
			}

			if HasFinalStatus(taskObject) {
				return taskObject, nil
			}
		}
	}
}
