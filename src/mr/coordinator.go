package mr

import (
	"log"
	"sync"
	"time"
)
import "net"
import "os"
import "net/rpc"
import "net/http"

type Coordinator struct {
	// Your definitions here.
	mu      sync.Mutex
	NReduce int        // reduce的数量
	Files   []string   // 待处理的文件
	TaskCh  chan *Task // 任务队列

	CoordinatorPhase CoordinatorPhase   // Coordinator当前所处阶段
	TaskStates       map[int]*TaskState // 每个任务的统计信息
	Intermediates    [][]string         // Map产生R个中间文件信息
}

type Task struct {
	File             string
	CoordinatorPhase CoordinatorPhase
	NReduce          int
	TaskNumber       int
	Intermediates    []string
}

type CoordinatorPhase int

const (
	Map CoordinatorPhase = iota
	Reduce
	Exit
	Wait
)

type TaskPhase int

const (
	Idle TaskPhase = iota
	InProgress
	Completed
)

type TaskState struct {
	TaskPhase TaskPhase
	Task      *Task
	StartTime time.Time
}

// Your code here -- RPC handlers for the worker to call.

// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
func (c *Coordinator) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

func (c *Coordinator) AssignTask(args *TaskArgs, reply *Task) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.TaskCh) > 0 {
		*reply = *<-c.TaskCh
		c.TaskStates[reply.TaskNumber].TaskPhase = InProgress
		c.TaskStates[reply.TaskNumber].StartTime = time.Now()
	} else if c.CoordinatorPhase == Exit {
		*reply = Task{
			CoordinatorPhase: Exit,
		}
	} else {
		*reply = Task{
			CoordinatorPhase: Wait, // 没有task就让worker 等待
		}
	}
	return nil
}

func (c *Coordinator) TaskCompleted(task *Task, reply *TaskReply) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if task.CoordinatorPhase != c.CoordinatorPhase || c.TaskStates[task.TaskNumber].TaskPhase == Completed {
		return nil // 因为worker写在同一个文件磁盘上，对于重复的结果要丢弃
	}
	c.TaskStates[task.TaskNumber].TaskPhase = Completed
	go c.processTaskResult(task)

	return nil
}

func (c *Coordinator) processTaskResult(task *Task) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch task.CoordinatorPhase {
	case Map:
		//收集intermediate信息
		for reduceTaskId, filePath := range task.Intermediates {
			c.Intermediates[reduceTaskId] = append(c.Intermediates[reduceTaskId], filePath)
		}
		if c.allTaskDone() {
			//获得所以map task后，进入reduce阶段
			c.createReduceTask()
			c.CoordinatorPhase = Reduce
		}
	case Reduce:
		if c.allTaskDone() {
			//获得所以reduce task后，进入exit阶段
			c.CoordinatorPhase = Exit
		}
	}
}

func (c *Coordinator) allTaskDone() bool {
	for _, task := range c.TaskStates {
		if task.TaskPhase != Completed {
			return false
		}
	}
	return true
}

func (c *Coordinator) createReduceTask() {
	c.TaskStates = make(map[int]*TaskState)
	for idx, files := range c.Intermediates {
		task := Task{
			//Input:         "",
			CoordinatorPhase: Reduce,
			NReduce:          c.NReduce,
			TaskNumber:       idx,
			Intermediates:    files,
		}
		c.TaskCh <- &task
		c.TaskStates[idx] = &TaskState{
			TaskPhase: Idle,
			//StartTime:     time.Time{},
			Task: &task,
		}
	}
}

// start a thread that listens for RPCs from worker.go
func (c *Coordinator) server() {
	rpc.Register(c)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
func (c *Coordinator) Done() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.CoordinatorPhase == Exit
}

func (c *Coordinator) createMapTask() {
	for idx, filename := range c.Files {
		task := &Task{
			File:             filename,
			CoordinatorPhase: Map,
			NReduce:          c.NReduce,
			TaskNumber:       idx,
			Intermediates:    make([]string, c.NReduce),
		}
		c.TaskCh <- task
		c.TaskStates[idx] = &TaskState{
			TaskPhase: Idle,
			Task:      task,
		}
	}
}

// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	c := Coordinator{
		mu:               sync.Mutex{},
		NReduce:          nReduce,
		Files:            files,
		TaskCh:           make(chan *Task, max(nReduce, len(files))),
		CoordinatorPhase: Map,
		TaskStates:       make(map[int]*TaskState, len(files)),
		Intermediates:    make([][]string, nReduce),
	}

	c.createMapTask()

	c.server()
	go c.catchTimeOut()

	return &c
}

func (c *Coordinator) catchTimeOut() {
	for {
		time.Sleep(5 * time.Second)
		c.mu.Lock()
		if c.CoordinatorPhase == Exit {
			c.mu.Unlock()
			return
		}
		for _, TaskState := range c.TaskStates {
			if TaskState.TaskPhase == InProgress && time.Now().Sub(TaskState.StartTime) > 10*time.Second {
				c.TaskCh <- TaskState.Task
				TaskState.TaskPhase = Idle
			}
		}
		c.mu.Unlock()
	}
}
