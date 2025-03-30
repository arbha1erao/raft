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
		log.Printf("info: node %d updating term from %d to %d during vote request",
			rn.id, rn.currentTerm, args.Term)
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

		// Reset election timeout by sending to heartbeat channel
		// Use non-blocking send to prevent potential deadlock
		select {
		case rn.heartbeatChan <- 1:
			// Successfully reset timeout
		default:
			// Channel is full, but that's OK - the buffer should handle it
		}
	} else {
		reply.VoteGranted = false
		log.Printf("info: node %d rejecting vote for candidate %d (already voted for %d in term %d)",
			rn.id, args.CandidateID, rn.votedFor, rn.currentTerm)
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
		log.Printf("info: node %d rejecting append entries from node %d (term %d < %d)",
			rn.id, args.LeaderID, args.Term, rn.currentTerm)
		reply.Term = rn.currentTerm
		reply.Success = false
		return nil
	}

	wasLeader := (rn.state == LEADER)
	wasCandidate := (rn.state == CANDIDATE)

	if args.Term > rn.currentTerm {
		log.Printf("info: node %d updating term from %d to %d during append entries",
			rn.id, rn.currentTerm, args.Term)
		rn.currentTerm = args.Term
		rn.votedFor = -1

		if rn.state != FOLLOWER {
			rn.state = FOLLOWER
			if wasLeader {
				log.Printf("info: leader %d stepping down due to higher term %d from node %d",
					rn.id, args.Term, args.LeaderID)
			} else if wasCandidate {
				log.Printf("info: candidate %d becoming follower due to term %d from node %d",
					rn.id, args.Term, args.LeaderID)
			}
		}
	}

	// Accept this sender as a leader when:
	// 1. It's from the same term (or higher, handled above)
	// 2. It's not the leader ourselves for this term
	if rn.state != LEADER {
		if rn.leaderID != args.LeaderID {
			log.Printf("info: node %d recognizing node %d as leader for term %d",
				rn.id, args.LeaderID, args.Term)

			rn.leaderID = args.LeaderID
		}
	}

	// Always reset election timer on valid append entries
	select {
	case rn.heartbeatChan <- 1:
		// Successfully reset timeout
	default:
		// FIX ME
		// Channel full but that's OK - the buffer should handle it now
	}

	reply.Term = rn.currentTerm
	reply.Success = true
	return nil
}
