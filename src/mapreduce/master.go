package mapreduce

import "container/list"
import "fmt"


type WorkerInfo struct {
	address string
	// You can add definitions here.
}
type JobState struct{
	WorkerName string  // store the Worker's Name,but is not used,just store this information
	JobMethod  JobType // map or reduce,now this element is not used almost,but just in case.
	JobNum     int    // this num of this job method
	isSuccess      bool   // True mean have finished, False mean failed
}

func CallWorker(mr *MapReduce, jopState chan JobState,worker string, doJobArgs *DoJobArgs,doJobReply *DoJobReply){
	// a RPC from master to Client, and do job
	ok := call(worker,"Worker.DoJob",doJobArgs,doJobReply)

	// record whether this job has been finished successful.
	if ok{
		jopState <- JobState{worker,doJobArgs.Operation,doJobArgs.JobNumber,true}
		mr.registerChannel <- worker
	}else{
		jopState <- JobState{worker,doJobArgs.Operation,doJobArgs.JobNumber,false}
	}
}

func ScheduleMapJob(mr *MapReduce){
	mapState := make(chan JobState,mr.nMap)
	// Schedule map job
	for mr.nCurMap < mr.nMap{
		var doJobArgs = DoJobArgs{mr.file,"Map",mr.nCurMap,mr.nReduce}
		var doJobReply = DoJobReply{false}
		//get worker Address
		workerAddress := <- mr.registerChannel

		// call the function to finsh this work
		go CallWorker(mr,mapState,workerAddress,&doJobArgs,&doJobReply)
		mr.nCurMap++
	}
	// Check all map job has success
	for i := 0; i < mr.nMap; {	// this can be return when all Map job finished successful
		state := <- mapState
		if state.isSuccess{
			i++
		}else{
			var doJobArgs = DoJobArgs{mr.file,"Map",state.JobNum,mr.nReduce}
			var doJobReply = DoJobReply{false}
			//get worker Address
			workerAddress := <-	mr.registerChannel
			go CallWorker(mr,mapState,workerAddress,&doJobArgs,&doJobReply)
		}
	}
}

func ScheduleReduceJob(mr *MapReduce){
	reduceState := make(chan JobState,mr.nReduce)
	// Schedule reduce job
	for mr.nCurReduce < mr.nReduce{
		var doJobArgs = DoJobArgs{mr.file,"Reduce",mr.nCurReduce,mr.nMap} 
		var doJobReply = DoJobReply{false}
		//get worker Address
		workerAddress := <- mr.registerChannel  
		go CallWorker(mr,reduceState,workerAddress,&doJobArgs,&doJobReply)
		mr.nCurReduce++
	}
	// Check all reduce job has success
	for i := 0; i < mr.nReduce;{
		state := <- reduceState
		if state.isSuccess{
			i++
		}else{
			var doJobArgs = DoJobArgs{mr.file,"Reduce",state.JobNum,mr.nMap} 
			var doJobReply = DoJobReply{false}
			//get worker Address
			workerAddress := <- mr.registerChannel  
			go CallWorker(mr,reduceState,workerAddress,&doJobArgs,&doJobReply)
		}
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
