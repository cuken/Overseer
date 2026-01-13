package task

import (
	"container/heap"
	"sync"

	"github.com/cuken/overseer/pkg/types"
)

// Queue is a priority queue for tasks with dependency resolution
type Queue struct {
	store    *Store
	pq       priorityQueue
	mu       sync.Mutex
	notEmpty chan struct{}
}

// NewQueue creates a new task queue
func NewQueue(store *Store) *Queue {
	q := &Queue{
		store:    store,
		pq:       make(priorityQueue, 0),
		notEmpty: make(chan struct{}, 1),
	}
	heap.Init(&q.pq)
	return q
}

// LoadPending loads all pending tasks into the queue
func (q *Queue) LoadPending() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	tasks, err := q.store.ListPending()
	if err != nil {
		return err
	}

	for _, task := range tasks {
		heap.Push(&q.pq, &queueItem{task: task, priority: task.Priority})
	}

	if len(q.pq) > 0 {
		q.signalNotEmpty()
	}

	return nil
}

// Enqueue adds a task to the queue
func (q *Queue) Enqueue(task *types.Task) {
	q.mu.Lock()
	defer q.mu.Unlock()

	heap.Push(&q.pq, &queueItem{task: task, priority: task.Priority})
	q.signalNotEmpty()
}

// Dequeue returns the highest priority task that has all dependencies satisfied
func (q *Queue) Dequeue() *types.Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.pq.Len() == 0 {
		return nil
	}

	// Find first task with satisfied dependencies
	for i := 0; i < q.pq.Len(); i++ {
		item := q.pq[i]
		if q.dependenciesSatisfied(item.task) {
			heap.Remove(&q.pq, i)
			return item.task
		}
	}

	return nil
}

// DequeueBlocking blocks until a task is available
func (q *Queue) DequeueBlocking() *types.Task {
	for {
		if task := q.Dequeue(); task != nil {
			return task
		}
		<-q.notEmpty
	}
}

// Len returns the number of tasks in the queue
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pq.Len()
}

// Peek returns the highest priority task without removing it
func (q *Queue) Peek() *types.Task {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.pq.Len() == 0 {
		return nil
	}

	return q.pq[0].task
}

// Remove removes a specific task from the queue by ID
func (q *Queue) Remove(taskID string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.pq {
		if item.task.ID == taskID {
			heap.Remove(&q.pq, i)
			return true
		}
	}

	return false
}

// UpdatePriority updates the priority of a task in the queue
func (q *Queue) UpdatePriority(taskID string, newPriority int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i, item := range q.pq {
		if item.task.ID == taskID {
			item.priority = newPriority
			item.task.Priority = newPriority
			heap.Fix(&q.pq, i)
			return true
		}
	}

	return false
}

// dependenciesSatisfied checks if all task dependencies are completed
func (q *Queue) dependenciesSatisfied(task *types.Task) bool {
	if len(task.Dependencies) == 0 {
		return true
	}

	for _, depID := range task.Dependencies {
		depTask, err := q.store.Load(depID)
		if err != nil {
			// Dependency not found, consider it unsatisfied
			return false
		}
		if depTask.State != types.StateCompleted {
			return false
		}
	}

	return true
}

func (q *Queue) signalNotEmpty() {
	select {
	case q.notEmpty <- struct{}{}:
	default:
	}
}

// queueItem wraps a task with its priority for the heap
type queueItem struct {
	task     *types.Task
	priority int
	index    int
}

// priorityQueue implements heap.Interface
type priorityQueue []*queueItem

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	// Higher priority comes first
	return pq[i].priority > pq[j].priority
}

func (pq priorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *priorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*queueItem)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *priorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}
