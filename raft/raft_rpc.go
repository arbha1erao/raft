package raft

type RequestVoteArgs struct {
	Term         int
	CandidateId  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

// TODO
func (rn *RaftNode) RequestVoteRPC(args RequestVoteArgs, reply *RequestVoteReply) error {
	return nil
}

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

// TODO
func (rn *RaftNode) AppendEntriesRPC(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	return nil
}
