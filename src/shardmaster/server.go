package shardmaster

import "net"
import "fmt"
import "net/rpc"
import "log"

import "paxos"
import "sync"
import "sync/atomic"
import "os"
import "syscall"
import "encoding/gob"
import "math/rand"
import "time"
type ShardMaster struct {
	mu         sync.Mutex
	l          net.Listener
	me         int
	dead       int32 // for testing
	unreliable int32 // for testing
	px         *paxos.Paxos

	configs []Config // indexed by config num
	minIns int   // record the min Ins this server knows
	recJoin map[int64]bool // record join mathod
	recLeave map[int64]bool // record leave mathod
}


type Op struct {
	// Your data here.
	Type string // the type, join,leave,move,query
	Shard int // the shard used in move
	GID int64 // the gid, used in join leave move
	Servers []string // the servers used in join
}


// this function mainly  exec  operation as lab4-B
func (sm *ShardMaster) ExecOp(args Op) {
	ins := sm.px.Max() + 1 // get the max instance
	for true {
		to := 20 * time.Millisecond
		sm.px.Start(ins,args) // start proposal
		decided,agree := sm.px.Status(ins) // get the status
		for decided != paxos.Decided { // judge decided 
			time.Sleep(to)
			decided,agree = sm.px.Status(ins)
			if to < time.Second {
				to = to + 20 * time.Millisecond
			}else{
				break
			}
		}
		if decided == paxos.Decided { // when decided 
			agreeOp,_ := agree.(Op)
			// judge whether this operation or proposal
			if agreeOp.Type != args.Type || agreeOp.GID != args.GID || agreeOp.Shard != args.Shard {
				// if not this proposal, start a new instance
				ins = sm.px.Max() +1
				continue
			}
			// get the missed instance
			for i := sm.minIns + 1; i<=ins;i++{
				hasDecided,val := sm.px.Status(i)
				for hasDecided != paxos.Decided {
					sm.px.Start(i,Op{Type:"Query",GID:0})
					time.Sleep(50 * time.Millisecond)
					hasDecided,val = sm.px.Status(i)
				}
				op,_ := val.(Op)
				// update sm.configs
				sm.UpdateConf(op)
			}
			sm.minIns = ins
			sm.px.Done(ins)
			break
		}
	}
}

func (sm *ShardMaster) UpdateConf(op Op) {
	if (op.Type == "Join"){
		sm.JoinHandle(op) // call join handle
		sm.recJoin[op.GID] = true
	}else if(op.Type == "Leave"){
		sm.LeaveHandle(op)
		sm.recLeave[op.GID] = true
	}else if(op.Type == "Move"){
		sm.MoveHandle(op)
	}else if(op.Type == "Query"){
	}else{
		fmt.Println("in UpdateCong, type error,type is", op.Type)
	}
}

func (sm *ShardMaster) JoinHandle(op Op){
	// this Join hit donot work very well
	if sm.JoinHit(op.GID){ // juege whether has operation
	//	return
	}
	confNum := len(sm.configs) // get the length of sm.configs
	latestConfig := sm.configs[confNum - 1] // get the latest config
	newConfig := Config{} // make a new config
	newConfig.Groups = map[int64]([]string){}
	newConfig.Num = confNum + 1
	for gid,servers := range latestConfig.Groups{ // copy the old groups to the new groups
		newConfig.Groups[gid] = servers
	}
	newConfig.Groups[op.GID] = op.Servers // join the new group
	allShards := make(map[int]bool) // get the all shards, just used for record
	for i := 0;i < NShards;i++{
		allShards[i] = true
	}
	counter := map[int64]int{} // record the counts of each gid
	groupNum := len(newConfig.Groups)
	aver := NShards/groupNum // get the average
	remain := NShards%groupNum // get the remain
	for i := 0; i < NShards; i++{ // judge one by one
		oldgid := latestConfig.Shards[i] // get the old shards relete to which gid
		if oldgid > 0 && counter[oldgid] < aver { // if this exsit this gid and this gid donot relete to average shards
			counter[oldgid] ++
			newConfig.Shards[i] = oldgid // let this shard relete to this gid
			delete(allShards,i) // this shard used
		}else if oldgid > 0 && counter[oldgid] == aver && remain > 0 {
			remain --
			counter[oldgid] ++
			newConfig.Shards[i] = oldgid
			delete(allShards,i)
		}else if oldgid == 0{ // if donnot exsit this gid
			newConfig.Shards[i] = op.GID // all shards relete to this gid
			delete(allShards,i)
		}
	}
	for shard,_ := range allShards{ // donot use all shard
		for gid,_ := range newConfig.Groups{ // judge all the new groups have average all average + 1 shards
			if counter[gid] < aver{
				newConfig.Shards[shard] = gid
				counter[gid]++
				delete(allShards,shard)
				break
			}else if counter[gid] == aver && remain > 0 {
				newConfig.Shards[shard] = gid
				counter[gid] ++
				delete(allShards,shard)
				break
			}
		}
	}
	if len(allShards) != 0{
		fmt.Println("in JoinHandleERROR!!!!!!!")
	}
	sm.configs = append(sm.configs,newConfig) // append newconfig to sm.configs

}

func (sm *ShardMaster) JoinHit(gid int64) bool{
	if ok,_ := sm.recJoin[gid];ok{
		return true
	}else{
		return false
	}
}

func (sm *ShardMaster) Join(args *JoinArgs, reply *JoinReply) error {
	// Your code herie.
	sm.mu.Lock()
	defer sm.mu.Unlock()
	// this Join hit donot work very well
	if sm.JoinHit(args.GID){ // juege whether has operation
//		return nil
	}
	execOp := Op{Type:"Join",GID:args.GID,Servers:args.Servers}
	sm.ExecOp(execOp)
	return nil
}

func (sm *ShardMaster) LeaveHandle(op Op){
	// this leave hit donot work very well
	if sm.LeaveHit(op.GID) {
	//	return
	}
	confNum := len(sm.configs)
	latestConfig := sm.configs[confNum -1]
	newConfig := Config{}
	newConfig.Groups = map[int64]([]string){}
	newConfig.Num = confNum + 1
	for gid,servers := range latestConfig.Groups{
		if gid != op.GID{ // if this gid equal op.GID,ingore it.
			newConfig.Groups[gid] = servers
		}
	}

	// the follew just as joinHandle
	allShards := make(map[int]bool)
	for i :=0;i< NShards;i++{
		allShards[i] = true
	}
	counter := map[int64]int{}
	groupNum := len(newConfig.Groups)
	aver := NShards/groupNum
	remain := NShards%groupNum

	for i := 0;i<NShards;i++ {
		oldgid := latestConfig.Shards[i]
		if oldgid >0 && oldgid!=op.GID &&counter[oldgid] < aver {
			counter[oldgid] ++
			newConfig.Shards[i] = oldgid
			delete(allShards,i)
		}else if oldgid > 0 && oldgid != op.GID && counter[oldgid] == aver &&remain >0{
			remain --
			counter[oldgid] ++
			newConfig.Shards[i] = oldgid
			delete(allShards,i)
		}else if oldgid == 0{
			newConfig.Shards[i] = 0
			delete(allShards,i)
		}
	}
	for shard,_ := range allShards {
		for gid,_ := range newConfig.Groups {
			if counter[gid] < aver {
				newConfig.Shards[shard] = gid
				counter[gid]++
				delete(allShards,shard)
				break
			}else if counter[gid] == aver && remain >0{
				newConfig.Shards[shard] = gid
				counter[gid] ++
				delete(allShards,shard)
				break
			}
		}
	}
	if len(allShards) != 0{
		fmt.Println("In LeaveHandle ERROR!!!!!!")
	}
	sm.configs = append(sm.configs,newConfig)
}


func (sm *ShardMaster) LeaveHit(gid int64) bool{
	if ok,_ := sm.recLeave[gid];ok{
		return true
	}else{
		return false
	}
}

func (sm *ShardMaster) Leave(args *LeaveArgs, reply *LeaveReply) error {
	// Your code here.
	sm.mu.Lock()
	defer sm.mu.Unlock()
	// this leave hit donot work very well
	if sm.LeaveHit(args.GID){
//		return nil
	}
	execOp := Op{Type:"Leave",GID:args.GID}
	sm.ExecOp(execOp)
	return nil
}


func (sm *ShardMaster) MoveHandle(op Op){
	confNum := len(sm.configs)
	newConfig := Config{}
	newConfig.Groups = map[int64]([]string){}
	newConfig.Num = confNum + 1
	// copy old groups to new groups
	for gid,servers := range sm.configs[confNum-1].Groups{
		newConfig.Groups[gid] = servers
	}
	// copy old shard-gid relation to new shards
	for nShard,gid := range sm.configs[confNum-1].Shards{
		if nShard != op.Shard { 
			newConfig.Shards[nShard] = gid
		}else{ // if the shard is we want to move,move it
			newConfig.Shards[nShard] = op.GID
		}
	}
	sm.configs = append(sm.configs,newConfig)
}

func (sm *ShardMaster) Move(args *MoveArgs, reply *MoveReply) error {
	// Your code here.
	sm.mu.Lock()
	defer sm.mu.Unlock()
	execOp := Op{Type:"Move",Shard:args.Shard,GID:args.GID}
	sm.ExecOp(execOp)
	return nil
}

func (sm *ShardMaster) Query(args *QueryArgs, reply *QueryReply) error {
	// Your code here.
	sm.mu.Lock()
	defer sm.mu.Unlock()
	execOp := Op{Type:"Query"}
	sm.ExecOp(execOp)
	// after exec it, return the require config
	confLen := len(sm.configs)
	if args.Num == -1 || args.Num >= confLen{ // if require is -1 or length than latest,return latest
		reply.Config = sm.configs[confLen-1]
	}else{
		for i:= 0; i < len(sm.configs);i++{ // else return the require
			if sm.configs[i].Num == args.Num{
				reply.Config = sm.configs[i]
			}
		}
	}
	return nil
}

// please don't change these two functions.
func (sm *ShardMaster) Kill() {
	atomic.StoreInt32(&sm.dead, 1)
	sm.l.Close()
	sm.px.Kill()
}

func (sm *ShardMaster) isdead() bool {
	return atomic.LoadInt32(&sm.dead) != 0
}

// please do not change these two functions.
func (sm *ShardMaster) setunreliable(what bool) {
	if what {
		atomic.StoreInt32(&sm.unreliable, 1)
	} else {
		atomic.StoreInt32(&sm.unreliable, 0)
	}
}

func (sm *ShardMaster) isunreliable() bool {
	return atomic.LoadInt32(&sm.unreliable) != 0
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Paxos to
// form the fault-tolerant shardmaster service.
// me is the index of the current server in servers[].
//
func StartServer(servers []string, me int) *ShardMaster {
	sm := new(ShardMaster)
	sm.me = me

	sm.configs = make([]Config,1)
	sm.configs[0].Groups = map[int64][]string{}
	sm.minIns = -1
	sm.recJoin = make(map[int64]bool)
	sm.recLeave = make(map[int64]bool)
	rpcs := rpc.NewServer()

	gob.Register(Op{})
	rpcs.Register(sm)
	sm.px = paxos.Make(servers, me, rpcs)

	os.Remove(servers[me])
	l, e := net.Listen("unix", servers[me])
	if e != nil {
		log.Fatal("listen error: ", e)
	}
	sm.l = l

	// please do not change any of the following code,
	// or do anything to subvert it.

	go func() {
		for sm.isdead() == false {
			conn, err := sm.l.Accept()
			if err == nil && sm.isdead() == false {
				if sm.isunreliable() && (rand.Int63()%1000) < 100 {
					// discard the request.
					conn.Close()
				} else if sm.isunreliable() && (rand.Int63()%1000) < 200 {
					// process the request but force discard of reply.
					c1 := conn.(*net.UnixConn)
					f, _ := c1.File()
					err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
					if err != nil {
						fmt.Printf("shutdown: %v\n", err)
					}
					go rpcs.ServeConn(conn)
				} else {
					go rpcs.ServeConn(conn)
				}
			} else if err == nil {
				conn.Close()
			}
			if err != nil && sm.isdead() == false {
				fmt.Printf("ShardMaster(%v) accept: %v\n", me, err.Error())
				sm.Kill()
			}
		}
	}()

	return sm
}
