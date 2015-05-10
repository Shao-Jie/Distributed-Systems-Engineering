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

import "os"
import "syscall"
import "sync"
import "sync/atomic"
import "fmt"
import "math/rand"
import "time"

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

type Proposer struct{
	maxSeq map[int]int // record the maxSeq have seen in each instance
	proposals map[int](map[int]*Proposal) // record the proposal of each sequence
	                                      // in each instance
}

type Proposal struct{
	ins int  // the instance number
	seq int // the proposal number in this seq
	maxAccSeq int // the max accepted proposal sequence
	val interface{} // proposal value. 
	preNum int // record how many peers reply in prepare phase
	accNum int // record how many peers reply in accept phase
	majority int // define the majority
}

type Acceptor struct{
	maxSeq map[int]int // record the max prepare seq in each instance
	acceptSeq map[int]int // record the acceptSeq in each instacde
	acceptVal map[int]interface{} // record the acceptval in each instance
}

type Learner struct{
	maxIns int // the max proposal learned
	learnVal map[int]interface{} // record the learnVal in each instance
	learnValBool map[int]bool // record  whether the  instance have record just learnval decrease
	learnPeer map[int](map[int]bool) // record this Ins has learn in which peeps, 
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

	proposer *Proposer // record proposer information
	acceptor *Acceptor // record acceptor information
	learner  *Learner  // record learner information
	doneInEachPeer map[int]int
	maxIns  int // the max instance have seen.
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
		return false
	}
	defer c.Close()

	err = c.Call(name, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}

// Generate Unique seq
func (px *Paxos) GenerateSeq(num int) int{
	return num*100 + px.me
}

// Define which num is Majority
func (px *Paxos) GetMajority(num int) int{
	return num/2 + 1
}

func (px *Paxos)PrepareHandler(args *PrepareArgs,reply *PrepareReply) error{
	ok,val := px.Status(args.Ins) // judge whether decided
	if ok==Decided {
		reply.Err = ErrDecided
		reply.LearnVal = val
		return nil
	}
	px.mu.Lock()
	defer px.mu.Unlock()
	if px.maxIns < args.Ins{ // update the max Ins have seen
		px.maxIns = args.Ins
	}

	if args.Seq > px.acceptor.maxSeq[args.Ins]{ // args.Seq large than max prepared sequence
		reply.Err = ACC
		reply.AccSeq = px.acceptor.acceptSeq[args.Ins]
		reply.AccVal = px.acceptor.acceptVal[args.Ins]
		px.acceptor.maxSeq[args.Ins] =args.Seq
	}else{ // else reject
		reply.Err = ErrREJ
		reply.MaxSeq = px.acceptor.maxSeq[args.Ins]
	}

	return nil
}


// Prepare phase
func (px *Paxos)Prepare(des int, proposal *Proposal){
	px.mu.Lock()
	prepareArgs := &PrepareArgs {Ins:proposal.ins, Seq:proposal.seq}
	prepareReply := &PrepareReply{}
	px.mu.Unlock()
	// judge me 
	if des != px.me{
		call(px.peers[des],"Paxos.PrepareHandler",prepareArgs,prepareReply)
	}else{
		px.PrepareHandler(prepareArgs,prepareReply)
	}
	px.mu.Lock() // lock var
	defer px.mu.Unlock()
	if prepareReply.Err == ACC { // if ACC
		proposal.preNum ++        // preNum increase
		if prepareReply.AccSeq > proposal.maxAccSeq{ // if prepareReply lage than max accSeq,update it
			proposal.maxAccSeq = prepareReply.AccSeq
			proposal.val = prepareReply.AccVal // record the max acc val
		}
		if proposal.preNum == proposal.majority { // when acc equal majority, propose accept
				for i := 0; i < len(px.peers); i++{
					go px.Accept(i,proposal) // be a new thread
				}
		}
	}else if prepareReply.Err == ErrREJ{ // when Reject 
		if px.proposer.maxSeq[proposal.ins] < prepareReply.MaxSeq{ // update the maxseq in the proposal
			px.proposer.maxSeq[proposal.ins] = prepareReply.MaxSeq
		}
	}else if prepareReply.Err == ErrDecided{
//		px.learner.learnVal[proposal.ins] = prepareReply.LearnVal // update learnval
		if px.learner.maxIns < proposal.ins{
			px.learner.maxIns = proposal.ins
		}

		for i := 0; i < len(px.peers); i++{
			go px.Learn(i,proposal.ins,prepareReply.LearnVal) // broadcast to all 
		}
	}
}

func (px *Paxos) AcceptHandle(args *AcceptArgs,reply *AcceptReply) error{
	ok,val := px.Status(args.Ins)
	if ok == Decided {
		reply.Err = ErrDecided
		reply.LearnVal = val
		return nil
	}
	px.mu.Lock()
	defer px.mu.Unlock()
	if px.maxIns < args.Ins{
		px.maxIns = args.Ins
	}
	if args.Seq >= px.acceptor.maxSeq[args.Ins]{ // update acceptseq and maxseq acceptval
		reply.Err = ACC
		px.acceptor.acceptSeq[args.Ins] = args.Seq
		px.acceptor.acceptVal[args.Ins] = args.Val
		px.acceptor.maxSeq[args.Ins] = args.Seq
	}else{
		reply.Err = ErrREJ
		reply.MaxSeq = px.acceptor.maxSeq[args.Ins]
	}

	return nil
}

// accept phase
func (px *Paxos) Accept(des int, proposal *Proposal){
	px.mu.Lock()
	acceptArgs := &AcceptArgs{Ins:proposal.ins,Seq:proposal.seq,Val:proposal.val}
	acceptReply := &AcceptReply{}
	px.mu.Unlock()

	if des != px.me{
		call(px.peers[des],"Paxos.AcceptHandle",acceptArgs,acceptReply)
	}else{
		px.AcceptHandle(acceptArgs,acceptReply)
	}
	px.mu.Lock()
	defer px.mu.Unlock()
	if acceptReply.Err == ACC { // if acc acceptNum increase
		proposal.accNum ++
		if proposal.accNum == proposal.majority{ // when accnum equal majority, start decide
			for i := 0; i < len(px.peers); i++{
				go px.Learn(i,proposal.ins,proposal.val)
			}
		}
	}else if acceptReply.Err == ErrREJ{ // if reject  update maxseq of this Ins
		if px.proposer.maxSeq[proposal.ins] < acceptReply.MaxSeq{
			px.proposer.maxSeq[proposal.ins] = acceptReply.MaxSeq
		}
	}else if acceptReply.Err == ErrDecided{
		for i := 0; i < len(px.peers); i++{
			go px.Learn(i,proposal.ins,acceptReply.LearnVal) // start learn 
		}
	}
}

func (px *Paxos) LearnHandler(args *LearnArgs,reply *LearnReply)error{
	px.mu.Lock()
	defer px.mu.Unlock()
	reply.Err = ACC
	if px.maxIns < args.Ins { // update maxIns has seen
		px.maxIns = args.Ins
	}
	if val,ok:= px.learner.learnVal[args.Ins];ok{
		if val != args.Val{ // this may happen??
			fmt.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!ERROR!!!!!!!!!!!!!!!!!!1")
		}
		return nil
	}
	px.learner.learnVal[args.Ins] = args.Val
	px.learner.learnValBool[args.Ins] = true
	if px.learner.maxIns < args.Ins{ // update learn maxIns
		px.learner.maxIns = args.Ins
	}
	if val,ok:= px.doneInEachPeer[args.Peer];ok{
		if val < args.DoneIns{
			px.doneInEachPeer[args.Peer] = args.DoneIns // update other peers' doneIns
		}
	}else{
		px.doneInEachPeer[args.Peer] = args.DoneIns
	}
	return nil
}

// learn prase
func (px *Paxos) Learn(des int, ins int, val interface{}){
	px.mu.Lock()
	// we need trans my doneIns to other peer
	learnArgs := &LearnArgs{Ins:ins, Val:val, Peer:px.me,DoneIns:px.doneInEachPeer[px.me]}
	learnReply := &LearnReply{}
	px.mu.Unlock()
	if des != px.me {
		call(px.peers[des],"Paxos.LearnHandler",learnArgs,learnReply)
	}else{
		px.LearnHandler(learnArgs,learnReply)
	}

	px.mu.Lock()
	defer px.mu.Unlock()
	if _,ok := px.learner.learnPeer[ins];!ok{
		px.learner.learnPeer[ins] = make(map[int]bool)
	}
	px.learner.learnPeer[ins][des] = true // record which peers have decide
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
	ins := seq // this sequence instance
	if px.maxIns < ins { // update the maxIns have seen
		px.maxIns = ins
	}
	// return immediate
	go func(){
		// this is a loop, in every proposal the seq must lager than last one
		for i:= 1; px.isdead() == false; i= px.proposer.maxSeq[ins]/100 + 1{
			if ins < px.Min(){ // if small than min, forget it.
				return
			}else if hasDecided,_:= px.Status(ins);hasDecided==Decided{ // if decided,return
				return
			}else{
				// propose a new proposal
				px.mu.Lock()
				proposal := &Proposal{ins:ins, seq:px.GenerateSeq(i),val:v,preNum:0,accNum:0,majority:px.GetMajority(len(px.peers))}
		/*		if px.proposer.proposals == nil{
					px.proposer.proposals = make(map[int](map[int]*Proposal))
				}*/
				if _,ok:= px.proposer.proposals[ins];!ok{
					px.proposer.proposals[ins] = make(map[int]*Proposal)
				}
				px.proposer.proposals[ins][proposal.seq] = proposal
				px.mu.Unlock()

				for j := 0; j < len(px.peers); j++{
					go px.Prepare(j,proposal)
				}

				time.Sleep(100 * time.Millisecond)
			}
		}
	}()
}



//
// the application on this machine is done with
// all instances <= seq.
//
// see the comments for Min() for more explanation.
//
func (px *Paxos) Done(seq int) {
	// Your code here.
	px.mu.Lock()
	for ins, peer := range px.learner.learnPeer{ // judge whether other peers are all learn this instance
		if len(peer) < len(px.peers) && seq > ins{ // if any peer not learn, decrease the doneins
			seq = ins
		}
	}
	px.doneInEachPeer[px.me] = seq
	for i := 0; i <= seq ;i++{ // release the resource
		delete(px.proposer.maxSeq,i)
		delete(px.proposer.proposals,i)
		delete(px.acceptor.maxSeq,i)
		delete(px.acceptor.acceptSeq,i)
		delete(px.acceptor.acceptVal,i)
		delete(px.learner.learnVal,i)
		delete(px.learner.learnValBool,i)
		delete(px.learner.learnPeer,i)
	}
	px.mu.Unlock()
}

//
// the application wants to know the
// highest instance sequence known to
// this peer.
//
func (px *Paxos) Max() int {
	// Your code here.
	px.mu.Lock()
	defer px.mu.Unlock()
	if px.isdead() == false{
		return px.maxIns // just return the maxIns have seen
	}else{
		return -1
	}
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
	defer px.mu.Unlock()
	if px.isdead() == false {
		min := -1
		for _,val := range px.doneInEachPeer{ // range in all pees get the minum num
			if val < 0{ // if no other information,just set min = -1
				min = -1
				break
			}
			if min < 0{
				min = val
			}else if min > val{ // get the min val
				min = val
			}
		}
		return min + 1 // return min+!
	}else{
		return 0
	}
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
	min := px.Min()
	px.mu.Lock()
	defer px.mu.Unlock()
	if px.isdead() == false{
		ins := seq
		if ins < min{
			return Forgotten,nil // have forget
		}
		if val,ok:= px.learner.learnVal[ins];ok{
			return Decided,val // return decided
		}else{
			return Pending,nil
		}
	}else{
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
	px.proposer = &Proposer{}
	px.proposer.maxSeq = make(map[int]int)
	px.proposer.proposals = make(map[int](map[int]*Proposal))
	px.acceptor = &Acceptor{}
	px.acceptor.maxSeq = make(map[int]int)
	px.acceptor.acceptSeq = make(map[int]int)
	px.acceptor.acceptVal = make(map[int](interface{}))
	px.learner = &Learner{maxIns : -1} // init -1
	px.learner.learnVal =make(map[int](interface{}))
	px.learner.learnValBool =make(map[int]bool)
	px.learner.learnPeer = make(map[int](map[int]bool))
	px.doneInEachPeer = make(map[int]int)
	px.maxIns = -1 // init -1
	for i := 0; i < len(px.peers);i++{
		px.doneInEachPeer[i] = -1 // init -1
	}
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
