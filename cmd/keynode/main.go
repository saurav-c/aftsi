package main

import (
	"errors"
	cmn "github.com/saurav-c/tasc/lib/common"
	kpb "github.com/saurav-c/tasc/proto/keynode"
	tpb "github.com/saurav-c/tasc/proto/tasc"
	log "github.com/sirupsen/logrus"
	"strings"
	"time"
)

func (k *KeyNode) readKey(tid string, key string, readSet []string, beginTs int64,
	lowerBound string) (string, []byte, []string, error) {
	var keyVersions *kpb.KeyVersionList

	k.CommittedVersionIndex.mutex.RLock()
	keyLock, ok := k.CommittedVersionIndex.locks[key]
	if ok {
		keyLock.RLock()
		keyVersions = k.CommittedVersionIndex.index[key]
		keyLock.RUnlock()
		k.CommittedVersionIndex.mutex.RUnlock()
	} else {
		// Version Index does not exist
		k.CommittedVersionIndex.mutex.RUnlock()

		start := time.Now()
		_, keyVersions = k.CommittedVersionIndex.create(key, k.StorageManager)
		end := time.Now()
		go k.Monitor.TrackStat(tid, "[READ] Create Commited Version State", end.Sub(start))
	}

	lowerBoundVersion := ""
	if lowerBound != "" {
		split := strings.Split(lowerBound, cmn.KeyDelimeter)
		lowerBoundVersion = split[1]
	}

	var timeoutStart time.Time
	first := true
	start := time.Now()
	for {
		for i := len(keyVersions.Versions) - 1; i >= 0; i-- {
			version := keyVersions.Versions[i]

			// Check lower bound
			if cmn.CompareKeyVersion(lowerBoundVersion, version) > 0 {
				break
			}

			splits := strings.Split(version, cmn.VersionDelimeter)
			vCommitTsStr, vTxnId := splits[0], splits[1]
			vCommitTs := cmn.Int64FromString(vCommitTsStr)

			// Check to make sure this version existed when Txn began
			if vCommitTs >= beginTs {
				continue
			}

			// Check compatibility of this txn's readSet with this versions's cowrittenset
			coWrittenSet, ok := k.isCompatibleVersion(vTxnId, readSet)
			if !ok {
				continue
			}

			end := time.Now()
			go k.Monitor.TrackStat(tid, "[READ] Compute Read Version", end.Sub(start))

			storageKeyVersion := key + cmn.KeyDelimeter + version
			return storageKeyVersion, nil, coWrittenSet, nil
		}

		if first {
			first = false
			timeoutStart = time.Now()
		}

		// No valid versions found
		if time.Now().Sub(timeoutStart) >= 20 * time.Millisecond {
			log.Errorf("Timed out, no valid versions found for %s", key)
			break
		}

		// Sleep and retry
		log.Debugf("Sleeping and retrying to find versions for %s", key)
		time.Sleep(5 * time.Millisecond)
	}
	return "", nil, nil, errors.New("no valid version found")
}

func (k *KeyNode) isCompatibleVersion(versionTid string, readSet []string) ([]string, bool) {
	writeSet, _ := k.CommittedTxnSet.get(versionTid)
	writeSetVersions := map[string]string{}
	for _, keyVersion := range writeSet.Keys {
		split := strings.Split(keyVersion, cmn.KeyDelimeter)
		key, version := split[0], split[1]
		writeSetVersions[key] = version
	}

	for _, readSetKey := range readSet {
		split := strings.Split(readSetKey, cmn.KeyDelimeter)
		key, readVersion := split[0], split[1]
		if writeSetVersion, ok := writeSetVersions[key]; ok &&
			cmn.CompareKeyVersion(writeSetVersion, readVersion) > 0 {
			return nil, false
		}
	}
	return writeSet.Keys, true
}

func (k *KeyNode) validate(tid string, beginTs int64, commitTs int64, keys []string) (action kpb.TransactionAction) {
	conflictChan := make(chan bool, 2)

	start := time.Now()
	go k.checkPendingConflicts(beginTs, commitTs, keys, conflictChan)
	go k.checkCommittedConflicts(beginTs, commitTs, keys, conflictChan)

	count := 0
	for conflict := range conflictChan {
		count++
		if conflict {
			log.Debugf("Found conflict, aborting transaction %s", tid)
			return kpb.TransactionAction_ABORT
		}
		if count == 2 {
			go log.Debugf("No conflicts found for transaction %s", tid)
			break
		}
	}
	end := time.Now()
	go k.Monitor.TrackStat(tid, "[COMMIT] Validation conflict check", end.Sub(start))

	version := cmn.Int64ToString(commitTs) + cmn.VersionDelimeter + tid

	var keyVersions []string
	for _, key := range keys {
		keyVersion := key + cmn.KeyDelimeter + version
		keyVersions = append(keyVersions, keyVersion)
	}
	pendingTxnSet := &tpb.TransactionWriteSet{
		Keys: keyVersions,
	}
	k.PendingTxnSet.put(tid, pendingTxnSet)

	start = time.Now()
	k.PendingVersionIndex.updateIndex(tid, keyVersions, true, k.StorageManager, k.Monitor,
		"[COMMIT] Storage Write Pending Index")
	end = time.Now()
	go k.Monitor.TrackStat(tid, "[COMMIT] Update Pending Index", end.Sub(start))

	return kpb.TransactionAction_COMMIT
}

func (k *KeyNode) checkPendingConflicts(beginTs int64, commitTs int64, keys []string, reportChan chan bool) {
	log.Infof("Inside check pending conflicts")
	for _, key := range keys {
		log.Infof("Checking conflict for %s", key)
		k.PendingVersionIndex.mutex.RLock()
		pLock, ok := k.PendingVersionIndex.locks[key]

		if !ok {
			log.Info("Pending KVI does not exist")
			k.PendingVersionIndex.mutex.RUnlock()
			pLock, _ = k.PendingVersionIndex.create(key, k.StorageManager)
			k.PendingVersionIndex.mutex.RLock()
		}

		pLock.RLock()
		pendingVersions := k.PendingVersionIndex.index[key]
		k.PendingVersionIndex.mutex.RUnlock()

		log.Info("Got the pending versions")
		for _, versions := range pendingVersions.Versions {
			split := strings.Split(versions, cmn.VersionDelimeter)
			versionCommitTsStr := split[0]
			versionCommitTs := cmn.Int64FromString(versionCommitTsStr)
			if beginTs < versionCommitTs && versionCommitTs < commitTs {
				pLock.RUnlock()
				reportChan <- true
				return
			}
		}
		pLock.RUnlock()
	}
	reportChan <- false
}

func (k *KeyNode) checkCommittedConflicts(beginTs int64, commitTs int64, keys []string, reportChan chan bool) {
	for _, key := range keys {
		k.CommittedVersionIndex.mutex.RLock()
		cLock, ok := k.CommittedVersionIndex.locks[key]
		if !ok {
			k.CommittedVersionIndex.mutex.RUnlock()
			cLock, _ = k.CommittedVersionIndex.create(key, k.StorageManager)
			k.CommittedVersionIndex.mutex.RLock()
		}

		cLock.RLock()
		committedVersions := k.CommittedVersionIndex.index[key]
		k.CommittedVersionIndex.mutex.RUnlock()

		for _, versions := range committedVersions.Versions {
			split := strings.Split(versions, cmn.VersionDelimeter)
			versionCommitTsStr := split[0]
			versionCommitTs := cmn.Int64FromString(versionCommitTsStr)
			if beginTs < versionCommitTs && versionCommitTs < commitTs {
				cLock.RUnlock()
				reportChan <- true
				return
			}
		}
		cLock.RUnlock()
	}
	reportChan <- false
}

func (k *KeyNode) endTransaction(tid string, action kpb.TransactionAction, writeSet []string) error {
	pendingWrites, _ := k.PendingTxnSet.get(tid)

	defer k.localGarbageCollect(tid, pendingWrites)

	if action == kpb.TransactionAction_ABORT {
		return nil
	}

	// Commit txn set and key versions
	txnWriteSet := &tpb.TransactionWriteSet{Keys:writeSet}
	k.CommittedTxnSet.put(tid, txnWriteSet)

	start := time.Now()
	k.CommittedVersionIndex.updateIndex(tid, pendingWrites.Keys, true, k.StorageManager, k.Monitor,
		"[END] Storage Write Committed Index")
	end := time.Now()
	go k.Monitor.TrackStat(tid, "[END] Update Committed Index", end.Sub(start))

	return nil
}

func (k *KeyNode) localGarbageCollect(tid string, pendingWrites *tpb.TransactionWriteSet) {
	go k.PendingTxnSet.remove(tid)
	go k.PendingVersionIndex.updateIndex(tid, pendingWrites.Keys, false, k.StorageManager, k.Monitor,
		"[END] Storage Write Pending Index")
}

func (k *KeyNode) shutdown() {
	k.LogFile.Sync()
	k.LogFile.Close()
}

func main() {
	keyNode, err := NewKeyNode()
	if err != nil {
		log.Fatalf("Could not start new Key Node %v\n", err)
	}

	log.Info("Started Key Node")

	go keyNode.Monitor.SendStats(1 * time.Second)

	defer keyNode.shutdown()

	keyNode.listener()
}
