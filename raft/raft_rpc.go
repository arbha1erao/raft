package raft

import "log"

type RequestVoteArgs struct {
	Term         int
	CandidateID  int
	LastLogIndex int
	LastLogTerm  int
}

type RequestVoteReply struct {
	Term        int
	VoteGranted bool
}

func (rn *RaftNode) RequestVoteRPC(args RequestVoteArgs, reply *RequestVoteReply) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	if args.Term < rn.currentTerm {
		reply.VoteGranted = false
		reply.Term = rn.currentTerm
		return nil
	}

	if args.Term > rn.currentTerm {
		rn.currentTerm = args.Term
		rn.votedFor = -1
		rn.state = FOLLOWER
		rn.leaderID = -1
	}

	if rn.votedFor == -1 || rn.votedFor == args.CandidateID {
		// FIX ME
		// In a full implementation, also check log up-to-date status here
		rn.votedFor = args.CandidateID
		reply.VoteGranted = true

		select {
		case rn.heartbeatChan <- 1:
		default:
		}
	} else {
		reply.VoteGranted = false
	}

	reply.Term = rn.currentTerm

	return nil
}

type AppendEntriesArgs struct {
	Term         int
	LeaderID     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
}

func (rn *RaftNode) AppendEntriesRPC(args AppendEntriesArgs, reply *AppendEntriesReply) error {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	if args.Term < rn.currentTerm {
		reply.Term = rn.currentTerm
		reply.Success = false
		return nil
	}

	if args.Term > rn.currentTerm {
		rn.currentTerm = args.Term
		rn.state = FOLLOWER
		rn.votedFor = -1
	}

	select {
	case rn.heartbeatChan <- 1:
	default:
		log.Println("warning: heartbeat channel blocked")
	}

	reply.Term = rn.currentTerm
	reply.Success = true

	return nil
}
