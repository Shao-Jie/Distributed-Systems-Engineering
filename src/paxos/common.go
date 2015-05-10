package paxos

const(
	ACC    = "Accept"
	ErrDecided = "ErrDecided" // has decided
	ErrREJ = "ErrReject"
)

type Err string

type PrepareArgs struct {
	Ins int // instance number
	Seq int // the sequence number of proposal
}

type PrepareReply struct {
	Err Err    // return ACC or ErrREJ
	MaxSeq int // if rejects return the maxseq
	AccSeq  int // if ACC return the max accept sequence
	AccVal interface{}  // if ACC return the Accept val
//	PreVal interface{} // the value has prepared
	LearnVal interface{} // if be decided return learnVal
}

type AcceptArgs struct{
	Ins int // the instance
	Seq int // the sequence of this instance
	Val interface{}
}

type AcceptReply struct{
	Err Err
	MaxSeq int // if rejects return the maxSeq
	LearnVal interface{} // if be decided return learnVal
}

type LearnArgs struct{
	Ins int   // which ins will learn
	Val interface{} // the value
	Peer int   // we need get peer's doneins
	DoneIns int // the peer's done ins
}

type LearnReply struct{
	Err Err
}
