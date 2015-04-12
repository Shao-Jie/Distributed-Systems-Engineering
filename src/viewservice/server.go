package viewservice

import "net"
import "net/rpc"
import "log"
import "time"
import "sync"
import "fmt"
import "os"
import "sync/atomic"

type ViewServer struct {
	mu       sync.Mutex
	l        net.Listener
	dead     int32 // for testing
	rpccount int32 // for testing
	me       string


	// Your declarations here.
	View  View       // record current View
	Witness string   // this var record the third client server
	timeRecord map[string] int32 // we donot record time directly,
															// just record times, 0 to DeadPings.
	Acked  bool  // record the primary whether be acked
	newView View // if the primary not acked, the new change view will 
	             // record in newView but not return untill primary acked
							 // and only be accessed by the primary
}


//
// server JudgePing  function
// in this function we judge the Ping Client Pinging

func (vs *ViewServer) JudgePing(args *PingArgs){
	if args.Viewnum == 0{ // present new connection or crash(reboot)
		if vs.Acked { // if Primary acked
			// now start judge whether is crash or reboot
			if vs.View.Primary == args.Me{ // judge Primary
				vs.View.Primary = vs.View.Backup // promote backup to primary
				vs.View.Backup = ""             // noting : witness cannot be primary directly
				vs.View.Viewnum ++
				vs.Acked = false
			}else if vs.View.Backup == args.Me{ // judge Backup
				vs.View.Backup = vs.Witness    // promote witness to backup
				vs.Witness = ""
				vs.View.Viewnum ++
				vs.Acked = false
			}else  // now we should judge whether is new connection
				if len(vs.View.Primary) == 0{ // refresh Primary
				vs.View.Viewnum ++
				vs.View.Primary = args.Me
				vs.Acked = false
			}else if len(vs.View.Backup) == 0{ // refresh Backup
				if vs.View.Primary != args.Me {
					vs.View.Viewnum ++
					vs.View.Backup = args.Me
					vs.Acked = false
				}
			}else if len(vs.Witness) == 0{ // refresh Witness
				if vs.View.Primary != args.Me && vs.View.Backup != args.Me{
					vs.Witness = args.Me
				}
			}

		}else{  // not acked,but have connect we should record in newView
						// noting : we cannot refresh Primary.
			if len(vs.View.Backup) == 0{
				vs.newView.Viewnum = vs.View.Viewnum + 1
				vs.newView.Primary = vs.View.Primary
				vs.newView.Backup = args.Me
				vs.Witness = ""
			}else if len(vs.Witness) == 0{
				vs.Witness = args.Me
			}
		}
	}else if args.Viewnum == vs.View.Viewnum{
		if args.Me == vs.View.Primary{ // now, the Primary is acked
			vs.Acked = true // refresh Acked
			if vs.View.Viewnum < vs.newView.Viewnum{ // if we have remaind View,
					vs.View = vs.newView                // refresh it.
			}
		}
	}
}

//
// server Ping RPC handler.
//
func (vs *ViewServer) Ping(args *PingArgs, reply *PingReply) error {

	// Your code here.
	vs.mu.Lock()
	vs.timeRecord[args.Me] = DeadPings // in this we need refresh timerecord
	vs.JudgePing(args)       // judge ping
	vs.mu.Unlock()
	reply.View = vs.View
	return nil
}

//
// server Get() RPC handler.
//
func (vs *ViewServer) Get(args *GetArgs, reply *GetReply) error {

	// Your code here.
	reply.View = vs.View

	//reply.View.Viewnum = vs.View.Viewnum
	//reply.View.Primary = vs.View.Primary
	//reply.View.Backup = vs.View.Backup
	return nil
}


//
// tick() is called once per PingInterval; it should notice
// if servers have died or recovered, and change the view
// accordingly.
//
func (vs *ViewServer) tick() {

	// Your code here.
	pFlag := 2   // record Primary state
							 // 2 : inited
							 // 1 : we have this identity
							 // 0 : this identity is timeout
	bFlag := 2   // record Backup state
	wFlag := 2   // record Witness state
	changed := 0 // record whether view have been changed
	vs.mu.Lock()
	if len(vs.View.Primary) != 0{
		vs.timeRecord[vs.View.Primary] --
		pFlag = 1                      // present we have Primary
		if vs.timeRecord[vs.View.Primary] <= 0{
			pFlag = 0                    // record Primary have dead
		}
	}
	if len(vs.View.Backup) != 0{
		vs.timeRecord[vs.View.Backup] --
		bFlag = 1
		if vs.timeRecord[vs.View.Backup] <= 0{
			bFlag = 0
		}
	}
	if len(vs.Witness) != 0{
		vs.timeRecord[vs.Witness] --
		wFlag = 1
		if vs.timeRecord[vs.Witness] <= 0{
			wFlag = 0
		}
	}
	// now, we judge whether some identities have timeout,and should change.
	if vs.Acked{ // this change must be happend when acked.
		if pFlag == 0{ // the primary has timeout
			if bFlag == 1{ // but we have backup
				delete(vs.timeRecord,vs.View.Primary)
				vs.View.Primary = vs.View.Backup // promote backup
				changed = 1  // now,this view is changed
				if wFlag == 1{ // promote witness to backup
											 // noting : witness cannot be primary directly 
					vs.View.Backup = vs.Witness
					vs.Witness = ""
				}else{
					vs.View.Backup = ""
				}
			}
		}

		if bFlag == 0{
			if wFlag == 1{
				delete(vs.timeRecord,vs.View.Backup)
				vs.View.Backup = vs.Witness
				vs.Witness = ""
				changed = 1
			}
		}
		if changed == 1{
			vs.View.Viewnum ++
		}
	}
	vs.mu.Unlock()
}

//
// tell the server to shut itself down.
// for testing.
// please don't change these two functions.
//
func (vs *ViewServer) Kill() {
	atomic.StoreInt32(&vs.dead, 1)
	vs.l.Close()
}

//
// has this server been asked to shut down?
//
func (vs *ViewServer) isdead() bool {
	return atomic.LoadInt32(&vs.dead) != 0
}

// please don't change this function.
func (vs *ViewServer) GetRPCCount() int32 {
	return atomic.LoadInt32(&vs.rpccount)
}

func StartServer(me string) *ViewServer {
	vs := new(ViewServer)
	vs.me = me
	// Your vs.* initializations here.
	vs.timeRecord = make(map[string]int32)
	vs.Acked = true
	// tell net/rpc about our RPC server and handlers.
	rpcs := rpc.NewServer()
	rpcs.Register(vs)

	// prepare to receive connections from clients.
	// change "unix" to "tcp" to use over a network.
	os.Remove(vs.me) // only needed for "unix"
	l, e := net.Listen("unix", vs.me)
	if e != nil {
		log.Fatal("listen error: ", e)
	}
	vs.l = l

	// please don't change any of the following code,
	// or do anything to subvert it.

	// create a thread to accept RPC connections from clients.
	go func() {
		for vs.isdead() == false {
			conn, err := vs.l.Accept()
			if err == nil && vs.isdead() == false {
				atomic.AddInt32(&vs.rpccount, 1)
				go rpcs.ServeConn(conn)
			} else if err == nil {
				conn.Close()
			}
			if err != nil && vs.isdead() == false {
				fmt.Printf("ViewServer(%v) accept: %v\n", me, err.Error())
				vs.Kill()
			}
		}
	}()

	// create a thread to call tick() periodically.
	go func() {
		for vs.isdead() == false {
			vs.tick()
			time.Sleep(PingInterval)
		}
	}()

	return vs
}
