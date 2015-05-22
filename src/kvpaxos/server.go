package kvpaxos

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

const Debug = 0

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug > 0 {
		log.Printf(format, a...)
	}
	return
}


type Op struct {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Type string // get or appent or put
	Key string
	Value string
	ClientID int64 // ClientID and reqNum make sure one require only exec once.
	ReqNum int64
}

type KVPaxos struct {
	mu         sync.Mutex
	l          net.Listener
	me         int
	dead       int32 // for testing
	unreliable int32 // for testing
	px         *paxos.Paxos

	// Your definitions here.
	data map[string]string // record the data (key value)
	recOp map[int64](map[int64]bool) // record the operate log (clientID(reqNum,bool))
	minIns int // record the minIns this server have
}

// judge this operation have execd
func (kv *KVPaxos) IsHit(clientID int64,reqNum int64) bool {
	if client,hasClient := kv.recOp[clientID];hasClient {
		if _,hasReq := client[reqNum];hasReq{
			return true
		}
	}
	return false
}

// update this operate record
func (kv *KVPaxos) UpdateRecord(op Op){
	if _,hasClient := kv.recOp[op.ClientID];!hasClient{
		kv.recOp[op.ClientID] = make(map[int64]bool)
	}
	kv.recOp[op.ClientID][op.ReqNum] = true
}

// update this data
func (kv *KVPaxos) UpdateData(op Op){
//	 fmt.Println("in kv.me--,UpdateData op --",kv.me,op)
	if !kv.IsHit(op.ClientID,op.ReqNum){
		if op.Type == "Put" {
			kv.data[op.Key] = op.Value
		}else if op.Type == "Append"{
			kv.data[op.Key] += op.Value
		}
		// when finish updatedata, then update record
		kv.UpdateRecord(op)
	}

//	fmt.Println("in kv.me--, UpdateData kv.data --",kv.me,kv.data)
}

func (kv *KVPaxos) ExecOp(args Op){
	// get the instance, this server will propose
	ins := kv.px.Max() + 1
	for true {
		fmt.Println("In kv.me--, ins --,args --",kv.me,ins,args)
		to := 20 * time.Millisecond
		kv.px.Start(ins,args)
		decided,agree := kv.px.Status(ins)
		// judge whether decided, if not loop.
		for decided !=paxos.Decided {
			fmt.Println("in decided loop kv.me--,ins--,op--,",kv.me,ins,args)
			time.Sleep(to)
			decided,agree = kv.px.Status(ins)
			if to <  time.Second {
				to = to + 20 * time.Millisecond
			}else{
				break
			}
		}
		if decided==paxos.Decided {
			agreeOp,_ := agree.(Op)
			// if decided judge this propose
			if agreeOp.Type != args.Type || agreeOp.ClientID != args.ClientID || agreeOp.ReqNum != args.ReqNum {
				// if not get new instance 
				ins = kv.px.Max() + 1
				continue
			}
			fmt.Println("in kv.me--, minIns --,ins--,",kv.me,kv.minIns,ins)
			// get the miss operation
			for i := kv.minIns + 1;i <= ins;i++{
				hasDecided,val := kv.px.Status(i)
				for hasDecided != paxos.Decided {
					kv.px.Start(i,Op{Type:"Get", Key:"0"})
					time.Sleep(50 * time.Millisecond)
					hasDecided,val = kv.px.Status(i)
				}
				fmt.Println("in loop ck.me--,ins--,op --",kv.me,i,args)
				op,_ := val.(Op)
				kv.UpdateData(op) // updata this missed opetation
			}
			kv.minIns = ins
			kv.px.Done(ins) // let the server forget it.
			break
		}
	}
	// delete the recop less than reqnum in this clientID
//	kv.DeleteRecOp(args.ClientID,args.ReqNum)
}

// delete no use record operation
// cannot pass the test,should be changed
func (kv *KVPaxos) DeleteRecOp(clientID int64,reqNum int64){
	if _,hasClient := kv.recOp[clientID];hasClient{
		var i int64
		for i = 0;i < reqNum;i++{
			delete(kv.recOp[clientID],i)
		}
	}
}

func (kv *KVPaxos) Get(args *GetArgs, reply *GetReply) error {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()
	// judge have exec this operation
	if kv.IsHit(args.ClientID,args.ReqNum){
		reply.Err = OK
		reply.Value = kv.data[args.Key]
		return nil
	}
	execOp := Op{Type:"Get",Key:args.Key,ClientID:args.ClientID,ReqNum:args.ReqNum}
	kv.ExecOp(execOp) // exec this operation
	reply.Err = OK // return ok
	reply.Value = kv.data[args.Key]
	return nil
}

func (kv *KVPaxos) PutAppend(args *PutAppendArgs, reply *PutAppendReply) error {
	// Your code here.
	kv.mu.Lock()
	defer kv.mu.Unlock()
	// judge have exec this operation
	if kv.IsHit(args.ClientID,args.ReqNum){
		reply.Err = OK
		return nil
	}
	execOp := Op{Type:args.Op,Key:args.Key,Value:args.Value,ClientID:args.ClientID,ReqNum:args.ReqNum}
	kv.ExecOp(execOp)
	reply.Err = OK

	return nil
}

// tell the server to shut itself down.
// please do not change these two functions.
func (kv *KVPaxos) kill() {
	DPrintf("Kill(%d): die\n", kv.me)
	atomic.StoreInt32(&kv.dead, 1)
	kv.l.Close()
	kv.px.Kill()
}

func (kv *KVPaxos) isdead() bool {
	return atomic.LoadInt32(&kv.dead) != 0
}

// please do not change these two functions.
func (kv *KVPaxos) setunreliable(what bool) {
	if what {
		atomic.StoreInt32(&kv.unreliable, 1)
	} else {
		atomic.StoreInt32(&kv.unreliable, 0)
	}
}

func (kv *KVPaxos) isunreliable() bool {
	return atomic.LoadInt32(&kv.unreliable) != 0
}

//
// servers[] contains the ports of the set of
// servers that will cooperate via Paxos to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
//
func StartServer(servers []string, me int) *KVPaxos {
	// call gob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	gob.Register(Op{})

	kv := new(KVPaxos)
	kv.me = me

	// Your initialization code here.
	kv.data = make(map[string]string)
	kv.recOp = make(map[int64]map[int64]bool)
	kv.minIns = -1

	rpcs := rpc.NewServer()
	rpcs.Register(kv)

	kv.px = paxos.Make(servers, me, rpcs)

	os.Remove(servers[me])
	l, e := net.Listen("unix", servers[me])
	if e != nil {
		log.Fatal("listen error: ", e)
	}
	kv.l = l


	// please do not change any of the following code,
	// or do anything to subvert it.

	go func() {
		for kv.isdead() == false {
			conn, err := kv.l.Accept()
			if err == nil && kv.isdead() == false {
				if kv.isunreliable() && (rand.Int63()%1000) < 100 {
					// discard the request.
					conn.Close()
				} else if kv.isunreliable() && (rand.Int63()%1000) < 200 {
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
			if err != nil && kv.isdead() == false {
				fmt.Printf("KVPaxos(%v) accept: %v\n", me, err.Error())
				kv.kill()
			}
		}
	}()

	return kv
}
