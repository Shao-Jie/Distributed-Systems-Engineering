package pbservice

import "net"
import "fmt"
import "net/rpc"
import "log"
import "time"
import "viewservice"
import "sync"
import "sync/atomic"
import "os"
import "syscall"
import "math/rand"



type PBServer struct {
	mu         sync.Mutex
	l          net.Listener
	dead       int32 // for testing
	unreliable int32 // for testing
	me         string
	vs         *viewservice.Clerk
	// Your declarations here.
	View       viewservice.View // record current view
	Data       map[string]string // record data map
	OpID       map[int64]bool   // record OpID ,if have recorded, denied it.
}

func (pb *PBServer) JudgeViewChanged(viewnum uint) bool {
	if pb.View.Viewnum > viewnum{ // if have view changed,return true 
		return true
	}else{
		return false
	}
}

func (pb *PBServer) Forward(args *ForwardArgs,reply *ForwardReply) error {
	pb.mu.Lock()   // this may happen concurrent. so should a lock.
	if pb.JudgeViewChanged(args.Viewnum) == false { // the view not change
		switch args.Op {  //judge operate
			case "Get":
				break
			case "Put":  // we have let put and append be the one put
				pb.Data[args.Key] = args.Value
				break
			default:
				break
		}
		reply.Err = OK
		pb.OpID[args.OpID] = true // record 
	}else{ // if view changed, the server has requested,may not be primary. 
		reply.Err = ErrWrongServer // so we should return wrong server, until the view not change.
	}
	pb.mu.Unlock()
	return nil
}

func (pb *PBServer) Get(args *GetArgs, reply *GetReply) error {

		pb.mu.Lock()
	// Your code here.
	if pb.View.Primary != pb.me{ // if me not primary, return
		reply.Err = ErrWrongServer
		pb.mu.Unlock()
		return nil
	}else{
		_,v := pb.OpID[args.OpID]
		if v == true{  // if have dealed it, return 
			reply.Err = ErrAlreadyOpID
			pb.mu.Unlock()
			return nil
		}
		// judge this key is in Data map
		_,ok := pb.Data[args.Key] // judge have this Key
		if !ok{
			reply.Err = ErrNoKey
			pb.mu.Unlock()
			return nil
		}
		// need connect to backup
		forwardArgs := &ForwardArgs{}
		forwardArgs.Op = "Get"
		forwardArgs.OpID = args.OpID
		forwardArgs.Viewnum = pb.View.Viewnum
		forwardReply := &ForwardReply{}
		if len(pb.View.Backup) != 0{ // exit backup
			cok := call(pb.View.Backup,"PBServer.Forward",forwardArgs,forwardReply) // forward RPC 
			if cok == false{  // cannot connect backup, maybe have partition network or not realiable. return errwrongserver
				reply.Err = ErrWrongServer
				pb.mu.Unlock()
				return nil
			}
		}
		if forwardReply.Err == ErrWrongServer{ // because viewchange, but me not heard, may have error,just return wrong server
			reply.Err = ErrWrongServer
			pb.mu.Unlock()
			return nil
		}
		reply.Value = pb.Data[args.Key] // get this value
		reply.Err = OK
		pb.OpID[args.OpID] = true
		pb.mu.Unlock()
	}
	return nil
}


func (pb *PBServer) PutAppend(args *PutAppendArgs, reply *PutAppendReply) error {
	// Your code here.
	pb.mu.Lock()
  if pb.View.Primary != pb.me{
		reply.Err = ErrWrongServer
		pb.mu.Unlock()
		return nil
	}else{
		// need transfer to backup
		forwardArgs := &ForwardArgs{}
		forwardReply := &ForwardReply{}
		for true{
			pb.mu.Unlock() // because this is a loop, this function cannot hold the lock always
			pb.mu.Lock()  // then locked
			_,v := pb.OpID[args.OpID]
			if v == true{  // if we have operated it,
				reply.Err = ErrAlreadyOpID // return ErralreadyOpID
				pb.mu.Unlock()
				return nil
			}
			forwardArgs.Op = "Put"// args.Op only be put
			forwardArgs.Key = args.Key
			if args.Op == "Put"{
				forwardArgs.Value = args.Value
			}else{
				forwardArgs.Value = pb.Data[args.Key] + args.Value // this is append, we append in this function directly,
			}                                                    // so in forward,we only change value with put
			forwardArgs.OpID = args.OpID
			forwardArgs.Viewnum = pb.View.Viewnum
			if len(pb.View.Backup) != 0{ // this backup maybe not change in the next loop,becauce network partition or unrealible
				cok := call(pb.View.Backup,"PBServer.Forward",forwardArgs,forwardReply)
				if cok == true{ // if we connect successful, then break
					break
				}
			}else{ // if no backup, then break
				break
			}
		}
		if forwardReply.Err == ErrWrongServer{ // the view have changed,return ErrWrongServer
			reply.Err = ErrWrongServer
			pb.mu.Unlock()
			return nil
		}
		// if all successful, change the value of primary
		if args.Op == "Put"{
			pb.Data[args.Key] = args.Value
			pb.OpID[args.OpID] = true
		}else{
			pb.Data[args.Key] += args.Value
			pb.OpID[args.OpID] = true
		}
		reply.Err = OK
		pb.mu.Unlock()
	}
	return nil
}

//
// trans data(and OpID) to backup in one time
//
func (pb *PBServer)TransDataToBackup( newView viewservice.View) bool {
	args := &TransDataArgs{}
	args.Data = pb.Data
	args.OpID = pb.OpID
	var reply TransDataReply
	ok := call(newView.Backup,"PBServer.UpdateData",args,&reply) // need judge have successful trans,if the net is unrealiable or partition, may failed
	if ok == false{
		return false
	}else{
		return true
	}
}

func (pb *PBServer)UpdateData(args *TransDataArgs,reply *TransDataReply) error {
	pb.mu.Lock()
	pb.Data = args.Data // copy data
	pb.OpID = args.OpID // copy opid
	pb.mu.Unlock()
	return nil
}

//
// ping the viewserver periodically.
// if view changed:
//   transition to new view.
//   manage transfer of state from primary to new backup.
//
func (pb *PBServer) tick() {

	// Your code here.
	pb.mu.Lock()
	args := &viewservice.PingArgs{}
	args.Me = pb.me  // Get my name 
	args.Viewnum = pb.View.Viewnum // Ping viewserver use current viewnum
	var reply viewservice.PingReply
	ok := call(pb.vs.GetServer(),"ViewServer.Ping",args,&reply)
  if ok == false{
		fmt.Errorf("Ping ViewServer error!")
	}else{
		if reply.View.Viewnum > pb.View.Viewnum{ // viewchange happen
			if(reply.View.Primary == pb.View.Primary && pb.View.Primary == pb.me){ // primary not change, so the backup mast change!
				// back must new, transfer data to Backup
				for true {
					if pb.TransDataToBackup(reply.View) == true{ // judge whether successful transfer
							break
					}
				}
			}else if(reply.View.Primary == pb.View.Backup && pb.View.Backup == pb.me){ // primary is new
				if len(reply.View.Backup) != 0{ // may have new backup, or not
					// transfer data to new backup
					for true{
						if pb.TransDataToBackup(reply.View) == true{
								break
						}
					}
				}
			}
			pb.View = reply.View
		}
	}
	pb.mu.Unlock()
}

// tell the server to shut itself down.
// please do not change these two functions.
func (pb *PBServer) kill() {
	atomic.StoreInt32(&pb.dead, 1)
	pb.l.Close()
}

func (pb *PBServer) isdead() bool {
	return atomic.LoadInt32(&pb.dead) != 0
}

// please do not change these two functions.
func (pb *PBServer) setunreliable(what bool) {
	if what {
		atomic.StoreInt32(&pb.unreliable, 1)
	} else {
		atomic.StoreInt32(&pb.unreliable, 0)
	}
}

func (pb *PBServer) isunreliable() bool {
	return atomic.LoadInt32(&pb.unreliable) != 0
}


func StartServer(vshost string, me string) *PBServer {
	pb := new(PBServer)
	pb.me = me
	pb.vs = viewservice.MakeClerk(me, vshost)
	// Your pb.* initializations here.
	pb.View.Viewnum = 0
	pb.Data = make(map[string]string)

	pb.OpID = make(map[int64]bool)
	rpcs := rpc.NewServer()
	rpcs.Register(pb)

	os.Remove(pb.me)
	l, e := net.Listen("unix", pb.me)
	if e != nil {
		log.Fatal("listen error: ", e)
	}
	pb.l = l

	// please do not change any of the following code,
	// or do anything to subvert it.

	go func() {
		for pb.isdead() == false {
			conn, err := pb.l.Accept()
			if err == nil && pb.isdead() == false {
				if pb.isunreliable() && (rand.Int63()%1000) < 100 {
					// discard the request.
					conn.Close()
				} else if pb.isunreliable() && (rand.Int63()%1000) < 200 {
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
			if err != nil && pb.isdead() == false {
				fmt.Printf("PBServer(%v) accept: %v\n", me, err.Error())
				pb.kill()
			}
		}
	}()

	go func() {
		for pb.isdead() == false {
			pb.tick()
			time.Sleep(viewservice.PingInterval)
		}
	}()

	return pb
}
