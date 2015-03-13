package mapreduce

import "container/list"
import "fmt"


type WorkerInfo struct {
	address string
	// You can add definitions here.
}

func CallWorker(mr *MapReduce,worker string, doJobArgs *DoJobArgs,doJobReply *DoJobReply){
	// a RPC from master to Client, and do job
	call(worker,"Worker.DoJob",doJobArgs,doJobReply)
	mr.registerChannel <- worker
}

func ScheduleMapJob(mr *MapReduce){
	// Schedule map job
	for mr.nCurMap < mr.nMap{
		var doJobArgs = DoJobArgs{mr.file,"Map",mr.nCurMap,mr.nReduce} 
		var doJobReply = DoJobReply{false}
		//get worker Address
		workerAddress := <- mr.registerChannel  
		go CallWorker(mr,workerAddress,&doJobArgs,&doJobReply)
		mr.nCurMap++
	}

}

func ScheduleReduceJob(mr *MapReduce){
	// Schedule reduce job
	for mr.nCurReduce < mr.nReduce{
		var doJobArgs = DoJobArgs{mr.file,"Reduce",mr.nCurReduce,mr.nMap} 
		var doJobReply = DoJobReply{false}
		//get worker Address
		workerAddress := <- mr.registerChannel  
		go CallWorker(mr,workerAddress,&doJobArgs,&doJobReply)
		mr.nCurReduce++
	}
}

// Clean up all workers by sending a Shutdown RPC to each one of them Collect
// the number of jobs each work has performed.
func (mr *MapReduce) KillWorkers() *list.List {
	l := list.New()
	for _, w := range mr.Workers {
		DPrintf("DoWork: shutdown %s\n", w.address)
		args := &ShutdownArgs{}
		var reply ShutdownReply
		ok := call(w.address, "Worker.Shutdown", args, &reply)
		if ok == false {
			fmt.Printf("DoWork: RPC %s shutdown error\n", w.address)
		} else {
			l.PushBack(reply.Njobs)
		}
	}
	return l
}

func (mr *MapReduce) RunMaster() *list.List {
	// Your code here
	ScheduleMapJob(mr)
	ScheduleReduceJob(mr)
	return mr.KillWorkers()
}
