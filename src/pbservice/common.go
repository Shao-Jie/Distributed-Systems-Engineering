package pbservice

const (
	OK             = "OK"
	ErrNoKey       = "ErrNoKey"
	ErrWrongServer = "ErrWrongServer"
	ErrViewServerDown = "ErrViewServerDown"
	ErrAlreadyOpID = "ErrAlreadyOpID"
)

type Err string

// Put or Append
type PutAppendArgs struct {
	Key   string
	Value string
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
	Op    string
	OpID  int64
}

type PutAppendReply struct {
	Err Err
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
	OpID int64
}

type GetReply struct {
	Err   Err
	Value string
}


// Your RPC definitions here.

type TransDataArgs struct{
	Data map[string]string
	OpID map[int64] bool
}

type TransDataReply struct{
	Err Err
}

type ForwardArgs struct{
	Key   string
	Value string
	OpID  int64
	Op    string
	Viewnum uint
}
type ForwardReply struct{
	Err Err
}
