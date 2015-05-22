package paxos

//
// Paxos library, to be included in an application.
// Multiple applications will run, each including
// a Paxos peer.
//
// Manages a sequence of agreed-on values.
// The set of peers is fixed.
// Copes with network failures (partition, msg loss, &c).
// Does not store anything persistently, so cannot handle crash+restart.
//
// The application interface:
//
// px = paxos.Make(peers []string, me string)
// px.Start(seq int, v interface{}) -- start agreement on new instance
// px.Status(seq int) (Fate, v interface{}) -- get info about an instance
// px.Done(seq int) -- ok to forget all instances <= seq
// px.Max() int -- highest instance seq known, or -1
// px.Min() int -- instances before this seq have been forgotten
//

import "net"
import "net/rpc"
import "log"
import "time"
import "strconv"

import "strings"
import "os"
import "syscall"
import "sync"
import "sync/atomic"
import "fmt"
import "math/rand"

// px.Status() return values, indicating
// whether an agreement has been decided,
// or Paxos has not yet reached agreement,
// or it was agreed but forgotten (i.e. < Min()).
type Fate int

const (
	Decided   Fate = iota + 1
	Pending        // not yet decided.
	Forgotten      // decided but forgotten.
)

type Instance struct {
	N_p    string      //highest prepared seen
	N_a    string      //highest accepted number
	V_a    interface{} //accpeted value
	Status Fate        //status
	//Pcount   int
	//Acount   int
	//Prepared bool
	//Accepted bool
}
type Paxos struct {
	mu         sync.Mutex
	l          net.Listener
	dead       int32 // for testing
	unreliable int32 // for testing
	rpcCount   int32 // for testing
	peers      []string
	me         int // index into peers[]
	// Your data here.
	ins  map[int]*Instance
	mins map[int]int
}
type PreArgs struct {
	Seq int
	Num string
	//Min    int
	//Caller string
}
type PreReply struct {
	Num      string
	Val      interface{}
	Rejected bool
	Decided  bool
	N_p      string
}
type AccArgs struct {
	Seq int
	Num string
	Val interface{}
}
type AccReply struct {
	Rejected bool
	Decided  bool
	Val      interface{}
}
type DecArgs struct {
	Seq int
	Val interface{}
	Me  int //piggyback num
	Min int //piggyback val
}
type DecReply struct {
}

//
// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the replys contents are only valid if call() returned true.
//
// you should assume that call() will time out and return an
// error after a while if it does not get a reply from the server.
//
// please use call() to send all RPCs, in client.go and server.go.
// please do not change this function.
//
func call(srv string, name string, args interface{}, reply interface{}) bool {
	c, err := rpc.Dial("unix", srv)
	if err != nil {
		err1 := err.(*net.OpError)
		if err1.Err != syscall.ENOENT && err1.Err != syscall.ECONNREFUSED {
			fmt.Printf("paxos Dial() failed: %v\n", err1)
		}
		//fmt.Println(err)
		return false
	}
	defer c.Close()

	err = c.Call(name, args, reply)
	if err == nil {
		return true
	}

	//fmt.Println(err)
	return false
}

//
// the application wants paxos to start agreement on
// instance seq, with proposed value v.
// Start() returns right away; the application will
// call Status() to find out if/when agreement
// is reached.
//
func (px *Paxos) Start(seq int, v interface{}) {
	// Your code here.
	////fmt.Println("Start:", seq, v)
	if seq >= px.Min() {
		//fmt.Println(px.me, "start()", seq)
		go px.Propose(seq, v)
	}
}
func (ins *Instance) String() string {
	//return fmt.Sprintf("N_p:%s,N_a:%s,V_a:%v,Status:%d", ins.N_p, ins.N_a, ins.V_a, ins.Status)
	return fmt.Sprintf("Status:%v", ins.V_a, ins.Status)
}
func (reply *PreReply) String() string {
	return fmt.Sprintf("Num:%s,Val:%v,Rejected:%v,Decided:%v", reply.Num, reply.Val, reply.Rejected, reply.Decided)
}
func (px *Paxos) GetMyMin() int {
	min := -1
	px.mu.Lock()
	v, e := px.mins[px.me]
	px.mu.Unlock()
	if e == true {
		min = v
	}
	return min
}
func (px *Paxos) GetRndPmt() []int {
	var rnd []int
	pcount := len(px.peers)
	rnd = make([]int, pcount)
	rep := map[int]int{}
	rr := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < pcount; i++ {
		rep[i] = i
	}
	for i := 0; i < pcount; i++ {
		tmp := rr.Intn(len(rep))
		key := 0
		for k, v := range rep {
			if key == tmp {
				rnd[i] = v
				delete(rep, k)
				break
			}
			key++
		}
	}
	return rnd
}
func (px *Paxos) GetProposalNum(src string) string {
	tail := "_" + strconv.FormatInt(time.Since(time.Date(2015, time.May, 1, 0, 0, 0, 0, time.UTC)).Nanoseconds(), 10)
	head := ""
	if src == "" {
		head = strconv.Itoa(px.me)
	} else {
		oldhead := strings.Split(src, "_")[0]
		index, _ := strconv.Atoi(oldhead)
		if index == px.me {
			head = strconv.Itoa(index)
		} else {
			head = strconv.Itoa(index + len(px.peers))
		}
	}
	return head + tail //reverse: nanosecond+px.me*i
}
func (px *Paxos) AddOrUpdateIns(seq int, val interface{}, status Fate) {
	px.mu.Lock()
	defer px.mu.Unlock()
	_, e := px.ins[seq]
	if e == false {
		px.ins[seq] = &Instance{V_a: val, Status: status}
	} else {
		px.ins[seq].V_a = val
		px.ins[seq].Status = status
	}
}
func (px *Paxos) SendPrepare(seq int, v interface{}, N_p string) (bool, bool, string, interface{}) { //Mojority?,Decided?,Value?
	//
	Max_N_p := N_p
	Max_N_a := "" //
	V_a := v      //
	//
	RecvCount := 0
	rnd := px.GetRndPmt()
	for _, i := range rnd {
		addr := px.peers[i]
		preArgs := &PreArgs{seq, N_p}
		var preReply PreReply
		ok := false
		times := 0
		//
		for !ok && times < 100 {
			if i != px.me {
				ok = call(addr, "Paxos.Prepare", preArgs, &preReply)
				//fmt.Println(px.me, "SendPrepare to", i, "result:", ok, "seq:", seq, "rejected:", preReply.Rejected)
			} else {
				ok = true
				px.Prepare(preArgs, &preReply)
			}
			if !ok {
				times++
				//time.Sleep(1 * time.Millisecond)
			}
		}
		if ok && !preReply.Rejected {
			RecvCount++
			if px.Bigger(preReply.Num, Max_N_a) {
				Max_N_a = preReply.Num
				V_a = preReply.Val
			}
			if RecvCount > len(px.peers)/2 {
				return true, false, N_p, V_a //cannot return,because has to piggyback min to all peers
			}
		} else if ok {
			if px.Bigger(preReply.N_p, Max_N_p) { //when came to live lock, to many RPCs...................
				Max_N_p = preReply.N_p
			}
			if preReply.Decided { //if this seq has been decided, then should set self seq decided
				px.SendDecide(seq, preReply.Val)
			}
			return !preReply.Rejected, preReply.Decided, Max_N_p, V_a
		}
	}
	return false, false, Max_N_p, V_a
}
func (px *Paxos) SendAccept(seq int, N_p string, v interface{}) (bool, bool) { //Mojority?,Decided?
	RecvCount := 0
	//fmt.Println(px.peers[px.me], "SendAccept", seq, N_p, v)
	rnd := px.GetRndPmt()
	for _, i := range rnd {
		addr := px.peers[i]
		accArgs := &AccArgs{seq, N_p, v}
		var accReply AccReply
		ok := false
		times := 0
		for !ok && times < 100 {
			if i != px.me {
				ok = call(addr, "Paxos.Accept", accArgs, &accReply)
				//fmt.Println(px.me, "SendAccept to", i, "result:", ok, "seq:", seq, "rejected:", accReply.Rejected)
			} else {
				ok = true
				px.Accept(accArgs, &accReply)
			}
			if !ok {
				times++
				//time.Sleep(1 * time.Millisecond)
			}
		}
		if ok && !accReply.Rejected {
			RecvCount++
			if RecvCount > len(px.peers)/2 {
				return true, false
			}
		} else if ok {
			if accReply.Decided {
				//px.AddOrUpdateIns(seq, accReply.Val, Decided)
				px.SendDecide(seq, accReply.Val)
			}
			return !accReply.Rejected, accReply.Decided
		}
	}
	return false, false
}
func (px *Paxos) TryToSendDecide(seq int, v interface{}, i int, addr string) {
	decArgs := &DecArgs{seq, v, px.me, px.GetMyMin()}
	var decReply DecReply
	ok := false
	times := 0
	for !ok && times < 100 {
		if i != px.me {
			ok = call(addr, "Paxos.Decide", decArgs, &decReply)
			//fmt.Println(px.me, "SendDecide", ok, seq)
		} else {
			px.Decide(decArgs, &decReply)
			ok = true
		}
		if !ok {
			times++
			time.Sleep(1 * time.Millisecond)
		}
	}
}
func (px *Paxos) SendDecide(seq int, v interface{}) {
	for i, addr := range px.peers {
		go px.TryToSendDecide(seq, v, i, addr)
	}
}
func (px *Paxos) Propose(seq int, v interface{}) {
	/*
		px.mu.Lock()
		px.ins[seq] = &Instance{Status: Pending} //create an instance first
		px.mu.Unlock()
		//fmt.Println(px.peers[px.me], "Propose:", seq, v)
	*/
	//
	isDecided := false
	isPrepared := false
	isAccepted := false
	//
	V_a := v
	N_p := ""
	//
	for !px.isdead() && !isDecided {
		N_p = px.GetProposalNum(N_p)
		isPrepared, isDecided, N_p, V_a = px.SendPrepare(seq, v, N_p)
		if !isDecided && isPrepared {
			isAccepted, isDecided = px.SendAccept(seq, N_p, V_a)
			if !isDecided && isAccepted {
				px.SendDecide(seq, V_a)
				isDecided = true
			}
		}
	}
}
func (px *Paxos) Bigger(a string, b string) bool {
	if a == b {
		return false
	}
	astr := strings.Split(a, "_")
	bstr := strings.Split(b, "_")
	if len(astr) < 2 {
		return false
	} else if len(bstr) < 2 {
		return true
	}
	if astr[0] != bstr[0] {
		ia, _ := strconv.Atoi(astr[0])
		ib, _ := strconv.Atoi(bstr[0])
		return ia > ib
	} else {
		ia, _ := strconv.Atoi(astr[1])
		ib, _ := strconv.Atoi(bstr[1])
		return ia > ib
	}
}
func (px *Paxos) Prepare(args *PreArgs, reply *PreReply) error {
	////fmt.Println("Prepare:", args
	px.mu.Lock()
	defer px.mu.Unlock()
	//px.mins[args.Caller] = args.Min //log the mins from caller's
	_, e := px.ins[args.Seq]
	//fmt.Println(px.peers[px.me], "Before prepare:", v, e)
	if e == false { //if not exist, accepto
		px.ins[args.Seq] = &Instance{Status: Pending}
	}
	reply.Num = px.ins[args.Seq].N_a
	reply.Val = px.ins[args.Seq].V_a
	reply.N_p = px.ins[args.Seq].N_p
	if px.ins[args.Seq].Status == Pending && px.Bigger(args.Num, px.ins[args.Seq].N_p) {
		px.ins[args.Seq].N_p = args.Num
		reply.Rejected = false
	} else {
		reply.Decided = px.ins[args.Seq].Status != Pending
		if reply.Decided {
			reply.Val = px.ins[args.Seq].V_a //if this seq has been decided, tell caller decided value
		}
		reply.Rejected = true
	}
	//fmt.Println(px.peers[px.me], "After prepare:", px.ins[args.Seq])
	return nil
}
func (px *Paxos) Accept(args *AccArgs, reply *AccReply) error {
	////fmt.Println("Accept:", args)
	px.mu.Lock()
	defer px.mu.Unlock()
	_, e := px.ins[args.Seq]
	//fmt.Println(px.peers[px.me], "Before Accept:", v, e)
	if e == false {
		px.ins[args.Seq] = &Instance{Status: Pending}
	}
	if px.ins[args.Seq].Status == Pending && (px.Bigger(args.Num, px.ins[args.Seq].N_p) || args.Num == px.ins[args.Seq].N_p) { //actually )>= is ok
		px.ins[args.Seq].N_p = args.Num
		px.ins[args.Seq].N_a = args.Num
		px.ins[args.Seq].V_a = args.Val
		reply.Rejected = false
		reply.Decided = false
	} else {
		reply.Decided = px.ins[args.Seq].Status != Pending
		if reply.Decided {
			reply.Val = px.ins[args.Seq].V_a
		}
		reply.Rejected = true
	}
	//fmt.Println(px.peers[px.me], "After Accept:", px.ins[args.Seq])
	//fmt.Println(px.peers[px.me], "Accept result:", reply)
	return nil
}

/*
func (px *Paxos) Prepare_ok(args *PreOkArgs, reply *NullReply) error {
	////fmt.Println("Prepare_ok:", args)
	if args.Num != -1 {
		px.mu.Lock()
		px.ins[args.Seq].Pcount++
		if px.ins[args.Seq].N_a < args.Num {
			px.ins[args.Seq].N_a = args.Num
			px.ins[args.Seq].V_a = args.Val
		}
		px.mu.Unlock()
	}
	px.mu.Lock()
	if px.ins[args.Seq].Pcount >= (len(px.peers)/2 + 1) { //send accept request
		for i, addr := range px.peers {
			accArgs := &AccArgs{args.Seq, px.ins[args.Seq].N_p, px.ins[args.Seq].V_a, px.peers[px.me]}
			var accReply NullReply
			if i != px.me {
				go call(addr, "Paxos.Accept", accArgs, &accReply)
			} else {
				go px.Accept(accArgs, &accReply)
			}
		}
	} else {
		//rejected
	}
	px.mu.Unlock()
	return nil
}
func (px *Paxos) Accept_ok(args *AccOkArgs, reply *NullReply) error {
	////fmt.Println("Accept_ok:", args)
	px.mu.Lock()
	defer px.mu.Unlock()
	_, e := px.ins[args.Seq]
	if e == false {
		px.ins[args.Seq] = &Instance{State: Pending}
	}
	px.ins[args.Seq].Acount++
	if px.ins[args.Seq].State == Pending && px.ins[args.Seq].Acount >= len(px.peers)/2+1 {
		for i, addr := range px.peers {
			decArgs := &DecArgs{args.Seq, px.ins[args.Seq].V_a}
			var decReply NullReply
			if i != px.me {
				go call(addr, "Paxos.Decide", decArgs, &decReply)
			} else {
				go px.Decide(decArgs, &decReply)
			}
		}
	}
	return nil
}*/
func (px *Paxos) Decide(args *DecArgs, reply *DecReply) error {
	//fmt.Println(px.me, "receive decide from:", args.Me, "seq:", args.Seq)
	px.mu.Lock()
	v, e := px.mins[args.Me]
	if !e || v < args.Min { //bcoz updates of the Min are came out of order
		px.mins[args.Me] = args.Min
	}
	//
	_, e2 := px.ins[args.Seq]
	if e2 == false {
		px.ins[args.Seq] = &Instance{}
	}
	px.ins[args.Seq].V_a = args.Val
	px.ins[args.Seq].Status = Decided
	//fmt.Println(px.me, "end do decide from:", args.Me, "seq:", args.Seq)
	px.mu.Unlock()
	return nil
}
func (px *Paxos) ForgetIns(min int) {
	//min := px.Min() //get the minimum of Mins
	//fmt.Println("After Done:", "Len(peers):", len(px.peers), px.mins, "Min():", min)
	//delete  seqs that minor than min
	//fmt.Println("before forget:", len(px.ins), min)
	px.mu.Lock()
	defer px.mu.Unlock()
	for k := range px.ins {
		if k < min {
			delete(px.ins, k)
		}
	}
	//fmt.Println("after forget:", len(px.ins), min)
	/////////////////////////
}

//
// the application on this machine is done with
// all instances <= seq.
//
// see the comments for Min() for more explanation.
//
func (px *Paxos) Done(seq int) {
	// Your code here.
	//fmt.Println(px.me, "Do Done with seq:", seq)
	//fmt.Println(px.me, "Before Done:", px.mins)
	px.mu.Lock()
	v, e := px.mins[px.me]
	if e == false || v < seq {
		for ik, iv := range px.ins {
			if ik <= v && iv.Status == Decided {
				iv.Status = Forgotten
			}
		}
		px.mins[px.me] = seq
	}
	px.mu.Unlock()
	//fmt.Println(px.me, "After Done:", px.mins)
	//px.ForgetIns() //
}

//
// the application wants to know the
// highest instance sequence known to
// this peer.
//
func (px *Paxos) Max() int {
	// Your code here.
	max := -1
	px.mu.Lock()
	defer px.mu.Unlock()
	for k, v := range px.ins {
		if k > max && v.Status == Decided {
			max = k
		}
	}
	return max
}

//
// Min() should return one more than the minimum among z_i,
// where z_i is the highest number ever passed
// to Done() on peer i. A peers z_i is -1 if it has
// never called Done().
//
// Paxos is required to have forgotten all information
// about any instances it knows that are < Min().
// The point is to free up memory in long-running
// Paxos-based servers.
//
// Paxos peers need to exchange their highest Done()
// arguments in order to implement Min(). These
// exchanges can be piggybacked on ordinary Paxos
// agreement protocol messages, so it is OK if one
// peers Min does not reflect another Peers Done()
// until after the next instance is agreed to.
//
// The fact that Min() is defined as a minimum over
// *all* Paxos peers means that Min() cannot increase until
// all peers have been heard from. So if a peer is dead
// or unreachable, other peers Min()s will not increase
// even if all reachable peers call Done. The reason for
// this is that when the unreachable peer comes back to
// life, it will need to catch up on instances that it
// missed -- the other peers therefor cannot forget these
// instances.
//
func (px *Paxos) Min() int {
	// You code here.
	px.mu.Lock()
	min := 0xffffffff
	for k := range px.peers {
		vmin, e := px.mins[k]
		if e == false {
			min = -1
			break
		} else if vmin < min {
			min = vmin
		}
	}
	px.mu.Unlock()
	px.ForgetIns(min + 1)
	//fmt.Println(px.me, "Mins:", px.mins, "Ins:", len(px.ins))
	return min + 1
}

//
// the application wants to know whether this
// peer thinks an instance has been decided,
// and if so what the agreed value is. Status()
// should just inspect the local peer state;
// it should not contact other Paxos peers.
//
func (px *Paxos) Status(seq int) (Fate, interface{}) {
	// Your code here.
	min := px.GetMyMin()
	px.mu.Lock()
	defer px.mu.Unlock()
	if seq <= min {
		return Forgotten, nil
	}
	v, e := px.ins[seq]
	if e == true { //required seq exists
		return px.ins[seq].Status, v.V_a
	} else {
		return Pending, nil
	}
}

//
// tell the peer to shut itself down.
// for testing.
// please do not change these two functions.
//
func (px *Paxos) Kill() {
	atomic.StoreInt32(&px.dead, 1)
	if px.l != nil {
		px.l.Close()
	}
}

//
// has this peer been asked to shut down?
//
func (px *Paxos) isdead() bool {
	return atomic.LoadInt32(&px.dead) != 0
}

// please do not change these two functions.
func (px *Paxos) setunreliable(what bool) {
	if what {
		atomic.StoreInt32(&px.unreliable, 1)
	} else {
		atomic.StoreInt32(&px.unreliable, 0)
	}
}

func (px *Paxos) isunreliable() bool {
	return atomic.LoadInt32(&px.unreliable) != 0
}

//
// the application wants to create a paxos peer.
// the ports of all the paxos peers (including this one)
// are in peers[]. this servers port is peers[me].
//
func Make(peers []string, me int, rpcs *rpc.Server) *Paxos {
	px := &Paxos{}
	px.peers = peers
	px.me = me

	// Your initialization code here.
	px.ins = make(map[int]*Instance)
	px.mins = make(map[int]int)

	if rpcs != nil {
		// caller will create socket &c
		rpcs.Register(px)
	} else {
		rpcs = rpc.NewServer()
		rpcs.Register(px)

		// prepare to receive connections from clients.
		// change "unix" to "tcp" to use over a network.
		os.Remove(peers[me]) // only needed for "unix"
		l, e := net.Listen("unix", peers[me])
		if e != nil {
			log.Fatal("listen error: ", e)
		}
		px.l = l

		// please do not change any of the following code,
		// or do anything to subvert it.

		// create a thread to accept RPC connections
		go func() {
			for px.isdead() == false {
				conn, err := px.l.Accept()
				if err == nil && px.isdead() == false {
					if px.isunreliable() && (rand.Int63()%1000) < 100 {
						// discard the request.
						conn.Close()
					} else if px.isunreliable() && (rand.Int63()%1000) < 200 {
						// process the request but force discard of reply.
						c1 := conn.(*net.UnixConn)
						f, _ := c1.File()
						err := syscall.Shutdown(int(f.Fd()), syscall.SHUT_WR)
						if err != nil {
							fmt.Printf("shutdown: %v\n", err)
						}
						atomic.AddInt32(&px.rpcCount, 1)
						go rpcs.ServeConn(conn)
					} else {
						atomic.AddInt32(&px.rpcCount, 1)
						go rpcs.ServeConn(conn)
					}
				} else if err == nil {
					conn.Close()
				}
				if err != nil && px.isdead() == false {
					fmt.Printf("Paxos(%v) accept: %v\n", me, err.Error())
				}
			}
		}()
	}

	return px
}
