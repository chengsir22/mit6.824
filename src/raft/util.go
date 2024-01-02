package raft

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

//// Debugging
//const Debug = false
//
//func DPrintf(format string, a ...interface{}) (n int, err error) {
//	if Debug {
//		log.Printf(format, a...)
//	}
//	return
//}

type logTopic string

const (
	logFolder = "logs"

	DError logTopic = "ERRO" // level = 3
	DWarn  logTopic = "WARN" // level = 2
	DInfo  logTopic = "INFO" // level = 1
	DDebug logTopic = "DBUG" // level = 0

	// 相关事件 level = 1
	DVote    logTopic = "VOTE" // voting
	DAppend  logTopic = "AppendEntries"
	DApply   logTopic = "APLY"
	DLeader  logTopic = "LEAD" // leader election
	DLog     logTopic = "LOG1" // sending log
	DLog2    logTopic = "LOG2" // receiving log
	DPersist logTopic = "PERS"
	DSnap    logTopic = "SNAP"
)

func getTopicLevel(topic logTopic) int {
	switch topic {
	case DError:
		return 3
	case DWarn:
		return 2
	case DInfo:
		return 1
	case DDebug:
		return 0
	default:
		return 1
	}
}

func getEnvLevel() int {
	v := os.Getenv("VERBOSE")
	//fmt.Println("v:", v)
	level := getTopicLevel(DError) + 1 // 未设置verbose时，默认为Error+1，不会打印任何log
	if v != "" {
		var err error
		level, err = strconv.Atoi(v)
		if err != nil {
			log.Fatalf("Invalid verbosity %v", v)
		}
	}
	return level
}

var logStart time.Time
var logLevel int

func init() {
	os.Setenv("VERBOSE", "0")

	logLevel = getEnvLevel()
	logStart = time.Now()

	// do not print verbose date
	//log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func LOG(peerId int, term int, topic logTopic, format string, a ...interface{}) {
	topicLevel := getTopicLevel(topic)
	if logLevel <= topicLevel {
		time := time.Since(logStart).Microseconds()
		time /= 100 // 0.1ms
		prefix := fmt.Sprintf("%06d T%04d [%v] S%d ", time, term, string(topic), peerId)
		format = prefix + format

		// 创建日志文件夹
		if _, err := os.Stat(logFolder); os.IsNotExist(err) {
			err := os.Mkdir(logFolder, 0755)
			if err != nil {
				log.Fatal(err)
			}
		}

		// 创建日志文件
		logFileName := fmt.Sprintf("%s/log_peer_%d.log", logFolder, peerId)
		file, err := os.OpenFile(logFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		// 创建Logger实例，关联到当前peer的日志文件
		logger := log.New(file, "", log.Ldate|log.Ltime|log.Lshortfile)

		logger.Printf(format, a...)
	}
}
