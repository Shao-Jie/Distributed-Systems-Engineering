package pbservice

import "viewservice"
import "net/rpc"
import "fmt"
import "crypto/rand"
import "math/big"
//import "strings"

type Clerk struct {
	vs *viewservice.Clerk
	// Your declarations here
	Primary string
}

// this may come in handy.
func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(vshost string, me string) *Clerk {
	ck := new(Clerk)
	ck.vs = viewservice.MakeClerk(me, vshost)
	// Your ck.* initializations here

	return ck
}

func (ck *Clerk)GetPrimary() bool{
	args := &viewservice.GetArgs{}
	var reply viewservice.GetReply
	ok := call(ck.vs.GetServer(),"ViewServer.Get",args,&reply)
	if ok == false{
		fmt.Errorf("Get primary error!")
		return false
	}else{
		ck.Primary = reply.View.Primary
	}
	return true
}

//
// call() sends an RPC to the rpcname handler on server srv
// with arguments args, waits for the reply, and leaves the
// reply in reply. the reply argument should be a pointer
// to a reply structure.
//
// the return value is true if the server responded, and false
// if call() was not able to contact the server. in particular,
// the reply's contents are only valid if call() returned true.
//
// you should assume that call() will return an
// error after a while if the server is dead.
// don't provide your own time-out mechanism.
//
// please use call() to send all RPCs, in client.go and server.go.
// please don't change this function.
//
func call(srv string, rpcname string,
	args interface{}, reply interface{}) bool {
	c, errx := rpc.Dial("unix", srv)
	if errx != nil {
		return false
	}
	defer c.Close()

	err := c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println(err)
	return false
}

//
// fetch a key's value from the current primary;
// if they key has never been set, return "".
// Get() must keep trying until it either the
// primary replies with the value or the primary
// says the key doesn't exist (has never been Put().
//


func (ck *Clerk) Get(key string) string {

	// Your code here.
	args := &GetArgs{}
	args.Key = key
	args.OpID = nrand()
	reply := &GetReply{}
	for true { // we cannot get the value once
		if len(ck.Primary) == 0{
			ck.GetPrimary()
		}
		ok :=	call(ck.Primary,"PBServer.Get",args,reply)
		if ok == false { // if cannot connect primary we should get new primary.
			live := ck.GetPrimary()
			if live == false{ // the viewserver is down, this test is over.
				return ErrViewServerDown
			}
		}else if reply.Err == ErrWrongServer{
			live := ck.GetPrimary()
			if live == false{
				return ErrViewServerDown
			}
		}else if reply.Err == ErrNoKey {
			return ErrNoKey
		}else if reply.Err == ErrAlreadyOpID{ // maybe the net is unreliable and cannot return value but record OpID
			args.OpID = nrand()
		}else if reply.Err == OK{
			return reply.Value
		}
	}
	return ""
}

//
// send a Put or Append RPC
//
func (ck *Clerk) PutAppend(key string, value string, op string) {

	// Your code here.
	args := &PutAppendArgs{}
	args.Key = key
	args.Value = value
	args.Op = op
	args.OpID = nrand()
	reply := &PutAppendReply{}
	for true { // we connot put successful just once.
		if len(ck.Primary) == 0{
			ck.GetPrimary()
		}
		ok := call(ck.Primary,"PBServer.PutAppend",args,reply)
		if ok == false{
			live := ck.GetPrimary()
			if live == false{ // when the viewserver is down, this test is over
				break
			}
		}else if reply.Err == ErrWrongServer{
			live := ck.GetPrimary()
			if live == false{
				break
			}
		}else if reply.Err == OK || reply.Err == ErrAlreadyOpID{ // if put successful or have put, break.
			break
		}
	}
}

//
// tell the primary to update key's value.
// must keep trying until it succeeds.
//
func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}

//
// tell the primary to append to key's value.
// must keep trying until it succeeds.
//
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}
