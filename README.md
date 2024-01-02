# mit6.824

[6.824 Home Page: Spring 2022](http://nil.csail.mit.edu/6.824/2022/)

# [MapReduce](https://www.youngzy.com/blog/2022/06/mit-6-824-lab-mr-2022/#more-1574)

![img](./assets/(null)-20240102152054061.(null))

```Go
// 如果任务超时，重置重新入队
func (c *Coordinator) catchTimeOut() {
    for {
        time.Sleep(5 * time.Second)
        c.mu.Lock()
        if c.CoordinatorPhase == Exit {
            c.mu.Unlock()
            return
        }
        for _, TaskState := range c.TaskStates {
            if TaskState.TaskPhase == InProgress && time.Now().Sub(TaskState.StartTime) > 10*time.Second {
                c.TaskCh <- TaskState.Task
                TaskState.TaskPhase = Idle
            }
        }
        c.mu.Unlock()
    }
}
```

# [Raft](http://nil.csail.mit.edu/6.824/2022/labs/lab-raft.html)

[Raft 论文导读 ｜ 硬核课堂](https://hardcore.feishu.cn/docs/doccnMRVFcMWn1zsEYBrbsDf8De#) [视频](https://www.bilibili.com/video/BV1CK4y127Lj/?spm_id_from=333.999.0.0&vd_source=143ed9e5b9a8342f01a329d8e2cbaed2) https://github.com/maemual/raft-zh_cn/blob/master/raft-zh_cn.md

![img](./assets/(null)-20240102152054345.(null))

## 领导者选举

![img](https://zq0zg0afdkw.feishu.cn/space/api/box/stream/download/asynccode/?code=NDkzM2M2ZTlhZjYzYjBjYTA2ZWRjZmVmNjFiYzg0YzBfa2xUZXVGOWFneFQ5ZW5oRjNlREt0dWhWSmhyUkl2aHlfVG9rZW46S0JpY2JxU0Zmb3MxS0J4Z3JjVGNzbHhUbldnXzE3MDQxNzk3NTQ6MTcwNDE4MzM1NF9WNA)

> 测试代码要求 Leader 每秒不能发超过几十次的心跳 RPC，也即你的心跳间隔不能太小。论文中的 5.2 小节提到过选举超时可以选取 150ms ~ 300ms 的超时间隔【可以略微调大点】，为了避免“活锁”，每个人都不断地选自己，需要让选举超时是随机的。这意味着你的心跳间隔不能大于 150ms（否则不能压制其他 Peer 发起选举）
>
> 测试代码要求在多数节点存活时，必须在 5s 内选出 Leader。需要注意的是，即使多数节点都存活，也不一定在一个轮次的选举 RPC 就能选出主（比如很小概率的有两个 Peer 同时发起选举并造成平票），因此要仔细选取选举超时参数，不能太大，否则规定时间内选不出 Leader。

### 选举

`RequestVote` RPC 请求中只会比较 term，会跳过谁的日志更 up-to-date 的比较（2B中实现）。

```Go
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    reply.Term = rf.currentTerm
    reply.VoteGranted = false
    // 对齐 term
    if args.Term < rf.currentTerm {
       LOG(rf.me, rf.currentTerm, DVote, "<- S%d, Reject voted, Higher term, T%d>T%d", args.CandidateId, rf.currentTerm, args.Term)
       return
    }
    if args.Term > rf.currentTerm {
       rf.becomeFollowerLocked(args.Term) // 重置votedFor和term
    }

    // check for votedFor
    if rf.votedFor != -1 {
       LOG(rf.me, rf.currentTerm, DVote, "<- S%d, Reject, Already voted to S%d", args.CandidateId, rf.votedFor)
       return
    }

    reply.VoteGranted = true
    rf.votedFor = args.CandidateId
    rf.resetElectionTimerLocked()
    LOG(rf.me, rf.currentTerm, DVote, "<- S%d, Vote granted", args.CandidateId)
}


func (rf *Raft) startElection(term int) {
    votes := 0
    askVoteFromPeer := func(peer int, args *RequestVoteArgs) {
       reply := &RequestVoteReply{}
       ok := rf.sendRequestVote(peer, args, reply)
       // handle the response
       rf.mu.Lock()
       defer rf.mu.Unlock()
       if !ok {
          LOG(rf.me, rf.currentTerm, DDebug, "-> S%d, Ask vote, Lost or error", peer)
          return
       }
       // 对齐 term ，votedFor=-1
       if reply.Term > rf.currentTerm {
          rf.becomeFollowerLocked(reply.Term) 
          return
       }
       // check the context
       if rf.contextLostLocked(Candidate, term) {
          LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Lost context, abort RequestVoteReply", peer)
          return
       }

       // count the votes
       if reply.VoteGranted {
          votes++
          if votes > len(rf.peers)/2 {
             rf.becomeLeaderLocked()
             go rf.replicationTicker(term)
          }
       }
    }

    rf.mu.Lock()
    defer rf.mu.Unlock()
    if rf.contextLostLocked(Candidate, term) {
       LOG(rf.me, rf.currentTerm, DVote, "Lost Candidate[T%d] to %s[T%d], abort RequestVote", term, rf.role, rf.currentTerm)
       return
    }

    for peer := 0; peer < len(rf.peers); peer++ {
       if peer == rf.me {
          votes++
          continue
       }

       args := &RequestVoteArgs{
          Term:        rf.currentTerm,
          CandidateId: rf.me,
       }

       go askVoteFromPeer(peer, args)
    }
}
```

### 上下文检查

“上下文”就是指 `Term` 和 `Role`。即在一个任期内，只要你的角色没有变化，就能放心地**推进状态机**。

```Go
func (rf *Raft) contextLostLocked(role Role, term int) bool {
        return !(rf.currentTerm == term && rf.role == role)
}
```

因为异步goroutine，因此每当线程新进入一个临界区时，要进行 Raft 上下文的检查。如果 Raft 的上下文已经被更改，要及时终止 goroutine，避免对状态机做出错误的改动。

### 心跳

`AppendEntries` RPC 请求只负责通过心跳压制其他 Peer 发起选举，心跳中**不包含**日志数据。

```Go
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    reply.Term = rf.currentTerm
    reply.Success = false
    // align the term
    if args.Term < rf.currentTerm {
       LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject log, Higher term, T%d<T%d", args.LeaderId, args.Term, rf.currentTerm)
       return
    }
    if args.Term >= rf.currentTerm {
       rf.becomeFollowerLocked(args.Term)
    }

    rf.resetElectionTimerLocked()
    reply.Success = true
}

// 是否成功地发起了一轮心跳
func (rf *Raft) startReplication(term int) bool {
    replicateToPeer := func(peer int, args *AppendEntriesArgs) {
       reply := &AppendEntriesReply{}
       ok := rf.sendAppendEntries(peer, args, reply)

       rf.mu.Lock()
       defer rf.mu.Unlock()
       if !ok {
          LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Lost or crashed", peer)
          return
       }

       // align the term
       if reply.Term > rf.currentTerm {
          rf.becomeFollowerLocked(reply.Term)
          return
       }
       
       // check context lost
      if rf.contextLostLocked(Leader, term) {
          LOG(rf.me, rf.currentTerm, DAppend, "-> S%d, Context Lost, T%d:Leader->T%d:%s", peer, term, rf.currentTerm, rf.role)
          return
      }
    }

    rf.mu.Lock()
    defer rf.mu.Unlock()

    if rf.contextLostLocked(Leader, term) {
       LOG(rf.me, rf.currentTerm, DLog, "Lost Leader[%d] to %s[T%d]", term, rf.role, rf.currentTerm)
       return false
    }

    for peer := 0; peer < len(rf.peers); peer++ {
       if peer == rf.me {
          continue
       }

       args := &AppendEntriesArgs{
          Term:     rf.currentTerm,
          LeaderId: rf.me,
       }
       go replicateToPeer(peer, args)
    }
    return true
}
```

## 日志同步

领导人（服务器）上的易失性状态 (becomeLeaderLocked后重新初始化)

| 参数         | 解释                                                         |
| ------------ | ------------------------------------------------------------ |
| nextIndex[]  | 对于每一台服务器，发送到该服务器的下一个日志条目的索引（初始值为领导人最后的日志条目的索引+1） |
| matchIndex[] | 对于每一台服务器，已知的已经复制到该服务器的最高日志条目的索引（初始值为0，单调递增） |

### 心跳增加日志复制

```Go
// RPC
// return failure if prevLog not matched
if args.PrevLogIndex > len(rf.log) {
    LOG(rf.me, rf.currentTerm, DAppend, "<- S%d, Reject log, Follower log too short, Len:%d < Prev:%d", args.LeaderId, len(rf.log), args.PrevLogIndex)
    return
}
// 任期不相等
if rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
    LOG(rf.me, rf.currentTerm, DAppend, "<- S%d, Reject log, Prev log not match, [%d]: T%d != T%d", args.LeaderId, args.PrevLogIndex, rf.log[args.PrevLogIndex].Term, args.PrevLogTerm)
    return
}
// append the leader log entries to local
rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...)
reply.Success = true
LOG(rf.me, rf.currentTerm, DAppend, "Follower accept logs: (%d, %d]", args.PrevLogIndex, args.PrevLogIndex+len(args.Entries))


// 日志返回 handle the reply ，probe the lower index if the prevLog not matched
if !reply.Success {
    // go back a term
    idx, term := args.PrevLogIndex, args.PrevLogTerm
    for idx > 0 && rf.log[idx].Term == term {
       idx--
    }
    rf.nextIndex[peer] = idx + 1
    LOG(rf.me, rf.currentTerm, DAppend, "Not match with S%d in %d, try next=%d", peer, args.PrevLogIndex, rf.nextIndex[peer])
    return
}
// update match/next index if log appended successfully
rf.matchIndex[peer] = args.PrevLogIndex + len(args.Entries) // important
rf.nextIndex[peer] = rf.matchIndex[peer] + 1
```

### 选举增加日志比较

比较最后一个 LogEntry，Term 高者更新，Term 同，Index 大者更新

```Go
func (rf *Raft) isMoreUpToDateLocked(candidateIndex, candidateTerm int) bool {
        l := len(rf.log)
        lastTerm, lastIndex := rf.log[l-1].Term, l-1
        LOG(rf.me, rf.currentTerm, DVote, "Compare last log, Me: [%d]T%d, Candidate: [%d]T%d", lastIndex, lastTerm, candidateIndex, candidateTerm)

        if lastTerm != candidateTerm {
                return lastTerm > candidateTerm
        }
        return lastIndex > candidateIndex
}
```

### 日志应用

leader接受命令

```Go
func (rf *Raft) Start(command interface{}) (int, int, bool) {
    rf.mu.Lock()
    defer rf.mu.Unlock()

    if rf.role != Leader {
       return 0, 0, false
    }
    rf.log = append(rf.log, LogEntry{
       CommandValid: true,
       Command:      command,
       Term:         rf.currentTerm,
    })
    LOG(rf.me, rf.currentTerm, DLeader, "Leader accept log [%d]T%d", len(rf.log)-1, rf.currentTerm)

    return len(rf.log) - 1, rf.currentTerm, true
}
```

使用go语言的条件变量，不能上一把大锁，因为你不知道applych会不会阻塞

```Go
func (rf *Raft) applicationTicker() {
    for !rf.killed() {
       rf.mu.Lock()
       rf.applyCond.Wait()
       entries := make([]LogEntry, 0)
       for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
          entries = append(entries, rf.log[i])
       }
       rf.mu.Unlock()

       for i, entry := range entries {
          rf.applyCh <- ApplyMsg{
             CommandValid: entry.CommandValid,
             Command:      entry.Command,
             CommandIndex: rf.lastApplied + 1 + i, // must be cautious
          }
       }

       rf.mu.Lock()
       LOG(rf.me, rf.currentTerm, DApply, "Apply log for [%d, %d]", rf.lastApplied+1, rf.lastApplied+len(entries))
       rf.lastApplied += len(entries)
       rf.mu.Unlock()
    }
}
```

## 持久化

### 日志回溯优化

1. 如果 Follower 日志过短，则`ConfilictTerm` 置空， `ConfilictIndex = len(rf.log)`。
2. 否则，将 `ConfilictTerm` 设置为 Follower 在 `Leader.PrevLogIndex` 处日志的 term；`ConfilictIndex` 设置为 `ConfilictTerm` 的第一条日志。

Leader 端使用上面两个新增字段的算法如下：

1. 如果 `ConfilictTerm` 为空，说明 Follower 日志太短，直接将 `nextIndex` 赋值为 `ConfilictIndex` 迅速回退到 Follower 日志末尾**。**
2. 否则，以 Leader 日志为准，跳过 `ConfilictTerm` 的所有日志；如果发现 Leader 日志中不存在 `ConfilictTerm` 的任何日志，则以 Follower 为准跳过 `ConflictTerm`，即使用 `ConfilictIndex`

```Go
if !reply.Success {
    prevIndex := rf.nextIndex[peer]
    if reply.ConfilictTerm == InvalidTerm {
       rf.nextIndex[peer] = reply.ConfilictIndex
    } else {
       firstIndex := rf.firstLogFor(reply.ConfilictTerm)
       if firstIndex != InvalidIndex {
          rf.nextIndex[peer] = firstIndex
       } else {
          rf.nextIndex[peer] = reply.ConfilictIndex
       }
    }
    // avoid unordered reply
    // avoid the late reply move the nextIndex forward again
    if rf.nextIndex[peer] > prevIndex {
       rf.nextIndex[peer] = prevIndex
    }

    LOG(rf.me, rf.currentTerm, DAppend, "-> S%d, Not match with at %d, try next=%d", peer, args.PrevLogIndex, rf.nextIndex[peer])
    return
}
```

### 日志提交优化

![img](./assets/(null)-20240102152054118.(null))

Leader 不能直接提交前任的命令前任，而要在本任期内发布命令后，通过“生效”本任期命令”来间接“追认”前序任期的相关命令。

```Go
// update the commitIndex
majorityMatched := rf.getMajorityIndexLocked()
if majorityMatched > rf.commitIndex && rf.log[majorityMatched].Term == rf.currentTerm {
    LOG(rf.me, rf.currentTerm, DApply, "Leader update the commit index %d->%d", rf.commitIndex, majorityMatched)
    rf.commitIndex = majorityMatched
    rf.applyCond.Signal()
}
```

## 日志压缩

1. 前面日志截断后 compact 成的 `snapshot`
2. 后面的剩余日志 `tailLog`
3. 两者的**分界线** `snapLastIdx` / `snapLastTerm` ，将 `tailLog` 中下标为 0 （对应 `snapLastIdx`）的日志留空，但给其的 `Term` 字段赋值 `snapLastTerm`，真正的下标从 1 （对应 `snapLastIdx`+1）开始。【所有的全局下标转到 `tailLog` 下标时，只需要减去 `snapLastIdx` 即可】

```Go
type RaftLog struct {
    snapLastIdx  int
    snapLastTerm int
    // contains [1, snapLastIdx]
    snapshot []byte
    // contains index (snapLastIdx, snapLastIdx+len(tailLog)-1] for real data
    // contains index snapLastIdx for mock log entry
    tailLog []LogEntry
}
```

![img](./assets/(null)-20240102152054117.(null))

### InstallSnapshot

```Go
// follower
func (rf *Raft) InstallSnapshot(args *InstallSnapshotArgs, reply *InstallSnapshotReply) {
    rf.mu.Lock()
    defer rf.mu.Unlock()
    LOG(rf.me, rf.currentTerm, DDebug, "<- S%d, RecvSnapshot, Args=%v", args.LeaderId, args.String())

    reply.Term = rf.currentTerm
    // align the term
    if args.Term < rf.currentTerm {
       LOG(rf.me, rf.currentTerm, DSnap, "<- S%d, Reject Snap, Higher Term: T%d>T%d", args.LeaderId, rf.currentTerm, args.Term)
       return
    }
    if args.Term >= rf.currentTerm { // = handle the case when the peer is candidate
       rf.becomeFollowerLocked(args.Term)
    }

    // check if there is already a snapshot contains the one in the RPC
    if rf.log.snapLastIdx >= args.LastIncludedIndex {
       LOG(rf.me, rf.currentTerm, DSnap, "<- S%d, Reject Snap, Already installed: %d>%d", args.LeaderId, rf.log.snapLastIdx, args.LastIncludedIndex)
       return
    }

    // install the snapshot in the memory/persister/app
    rf.log.installSnapshot(args.LastIncludedIndex, args.LastIncludedTerm, args.Snapshot)
    rf.persistLocked()
    rf.snapPending = true
    rf.applyCond.Signal()
}


// leader
func (rf *Raft) installToPeer(peer, term int, args *InstallSnapshotArgs) {
    reply := &InstallSnapshotReply{}
    ok := rf.sendInstallSnapshot(peer, args, reply)

    rf.mu.Lock()
    defer rf.mu.Unlock()
    if !ok {
       LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Lost or crashed", peer)
       return
    }
    LOG(rf.me, rf.currentTerm, DDebug, "-> S%d, SendSnapshot, Reply=%v", peer, reply.String())

    // align the term
    if reply.Term > rf.currentTerm {
       rf.becomeFollowerLocked(reply.Term)
       return
    }
    // check context lost
    if rf.contextLostLocked(Leader, term) {
       LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Context Lost, T%d:Leader->T%d:%s", peer, term, rf.currentTerm, rf.role)
       return
    }

    // update match and next index，防止rpc返回乱序
    if args.LastIncludedIndex > rf.matchIndex[peer] {
       rf.matchIndex[peer] = args.LastIncludedIndex
       rf.nextIndex[peer] = rf.matchIndex[peer] + 1
    }
}
```
