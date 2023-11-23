package mr

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"time"
)
import "log"
import "net/rpc"
import "hash/fnv"

// Map functions return a slice of KeyValue.
type KeyValue struct {
	Key   string
	Value string
}

// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

func getTask() Task {

	// declare an argument structure.
	args := TaskArgs{}

	// declare a reply structure.
	reply := Task{}
	// args是空的TaskArgs{}不需要参数
	// reply是返回的Task信息

	// send the RPC request, wait for the reply.
	ok := call("Coordinator.AssignTask", &args, &reply)

	if ok {
		//fmt.Println(reply)
	} else {
		fmt.Println("call failed!!!")
	}
	return reply
}

// main/mrworker.go calls this function.
func Worker(mapf func(string, string) []KeyValue,
	reducef func(string, []string) string) {

	// Your worker implementation here.
	for {
		task := getTask()
		switch task.CoordinatorPhase {
		case Map:
			//执行map
			mapper(&task, mapf)
		case Reduce:
			//执行reduce
			reducer(&task, reducef)
		case Wait:
			//休息一下
			fmt.Println("暂时没有任务，但总任务还未完成，请等待5s")
			time.Sleep(5 * time.Second)
		case Exit:
			//退出循环
			return
		}
	}
	// uncomment to send the Example RPC to the coordinator.
	// CallExample()
}

func mapper(task *Task, mapf func(string, string) []KeyValue) {
	//读取文件
	file, err := os.Open(task.File)
	if err != nil {
		log.Fatalf("cannot open %v", task.File)
	}
	content, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalf("cannot read %v", task.File)
	}
	file.Close()
	//执行map
	kva := mapf(task.File, string(content))
	//将结果写入中间文件
	buffer := make([][]KeyValue, task.NReduce)
	for _, tmp := range kva {
		slot := ihash(tmp.Key) % task.NReduce
		buffer[slot] = append(buffer[slot], tmp)
	}

	mapOutput := make([]string, 0)
	for i := 0; i < task.NReduce; i++ {
		mapOutput = append(mapOutput, WriteToLocalFile(task.TaskNumber, i, &buffer[i]))
	}

	task.Intermediates = mapOutput
	TaskCompleted(task)
}

// WriteToLocalFile 为了确保在发生崩溃时没有人观察到部分写入的文件，MapReduce 论文提到了使用
// 临时文件并在完全写入后自动 重命名的技巧。您可以使用 ioutil.TempFile创建临时文件，并使用
// os.Rename 以原子方式重命名它。
func WriteToLocalFile(x int, y int, kvs *[]KeyValue) string {
	dir, _ := os.Getwd()
	tmpFile, err := ioutil.TempFile(dir, "mr-tmp-*")
	if err != nil {
		log.Fatal("创建临时文件失败", err)
	}
	enc := json.NewEncoder(tmpFile)
	for _, kv := range *kvs {
		if err := enc.Encode(&kv); err != nil {
			log.Fatal("Failed to write kv pair", err)
		}
	}
	tmpFile.Close()
	outputName := fmt.Sprintf("mr-%d-%d", x, y)
	os.Rename(tmpFile.Name(), outputName)
	return filepath.Join(dir, outputName)
}

func TaskCompleted(task *Task) {
	reply := TaskReply{}
	call("Coordinator.TaskCompleted", task, &reply)
}

func reducer(task *Task, reducef func(string, []string) string) {
	// 读取intermediate的kv，并排序
	intermediate := *readFormLocalFile(task.Intermediates)
	sort.Slice(intermediate, func(i, j int) bool {
		return intermediate[i].Key < intermediate[j].Key
	})

	dir, _ := os.Getwd()
	tempFile, err := ioutil.TempFile(dir, "mr-tmp-*")
	if err != nil {
		log.Fatal("Fail to create temp file", err)
	}

	i := 0
	for i < len(intermediate) {
		//将相同的key放在一起分组合并
		j := i + 1
		for j < len(intermediate) && intermediate[j].Key == intermediate[i].Key {
			j++
		}
		values := []string{}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}
		//交给reducef，拿到结果
		output := reducef(intermediate[i].Key, values)
		//写到对应的output文件
		fmt.Fprintf(tempFile, "%v %v\n", intermediate[i].Key, output)
		i = j
	}
	tempFile.Close()
	oname := fmt.Sprintf("mr-out-%d", task.TaskNumber)
	os.Rename(tempFile.Name(), oname)
	// task.Output = oname
	TaskCompleted(task)
}

func readFormLocalFile(files []string) *[]KeyValue {
	kva := []KeyValue{}
	for _, filepath := range files {
		file, err := os.Open(filepath)
		if err != nil {
			log.Fatal("不能打开文件"+filepath, err)
		}
		dec := json.NewDecoder(file)
		for {
			var kv KeyValue
			if err := dec.Decode(&kv); err != nil {
				break
			}
			kva = append(kva, kv)
		}
		file.Close()
	}
	return &kva
}

// example function to show how to make an RPC call to the coordinator.
//
// the RPC argument and reply types are defined in rpc.go.
func CallExample() {

	// declare an argument structure.
	args := ExampleArgs{}

	// fill in the argument(s).
	args.X = 99

	// declare a reply structure.
	reply := ExampleReply{}

	// send the RPC request, wait for the reply.
	// the "Coordinator.Example" tells the
	// receiving server that we'd like to call
	// the Example() method of struct Coordinator.
	ok := call("Coordinator.Example", &args, &reply)
	if ok {
		// reply.Y should be 100.
		fmt.Printf("reply.Y %v\n", reply.Y)
	} else {
		fmt.Printf("call failed!\n")
	}
}

// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}
