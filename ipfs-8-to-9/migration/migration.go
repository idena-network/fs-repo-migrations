// package mg8 contains the code to perform 8-9 repository migration in
// go-ipfs. This performs a switch to raw multihashes for all keys in the
// go-ipfs datastore (https://github.com/ipfs/go-ipfs/issues/6815).
package mg8

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	migrate "github.com/ipfs/fs-repo-migrations/go-migrate"
	lock "github.com/ipfs/fs-repo-migrations/ipfs-1-to-2/repolock"
	"github.com/ipfs/fs-repo-migrations/mfsr"
	"github.com/ipfs/go-datastore/namespace"

	log "github.com/ipfs/fs-repo-migrations/stump"
	ds "github.com/ipfs/go-datastore"
	"github.com/ipfs/go-ipfs/plugin/loader"
	fsrepo "github.com/ipfs/go-ipfs/repo/fsrepo"
)

const backupFile = "8-to-9-cids.txt"

// Migration implements the migration described above.
type Migration struct{}

// Versions returns the current version string for this migration.
func (m Migration) Versions() string {
	return "8-to-9"
}

// Reversible returns true.
func (m Migration) Reversible() bool {
	return true
}

// lock the repo
func (m Migration) lock(opts migrate.Options) (io.Closer, error) {
	log.VLog("locking repo at %q", opts.Path)
	return lock.Lock2(opts.Path)
}

// open the repo
func (m Migration) open(opts migrate.Options) (ds.Batching, error) {
	log.VLog("  - loading repo configurations")
	plugins, err := loader.NewPluginLoader(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("error loading plugins: %s", err)
	}

	if err := plugins.Initialize(); err != nil {
		return nil, fmt.Errorf("error initializing plugins: %s", err)
	}

	if err := plugins.Inject(); err != nil {
		return nil, fmt.Errorf("error injecting plugins: %s", err)
	}

	cfg, err := fsrepo.ConfigAt(opts.Path)
	if err != nil {
		return nil, err
	}

	dsc, err := fsrepo.AnyDatastoreConfig(cfg.Datastore.Spec)
	if err != nil {
		return nil, err
	}

	return dsc.Create(opts.Path)
}

// Apply runs the migration and writes a log file that can be used by Revert.
func (m Migration) Apply(opts migrate.Options) error {
	log.Verbose = opts.Verbose
	log.Log("applying %s repo migration", m.Versions())

	lk, err := m.lock(opts)
	if err != nil {
		return err
	}
	defer lk.Close()

	repo := mfsr.RepoPath(opts.Path)

	log.VLog("  - verifying version is '8'")
	if err := repo.CheckVersion("8"); err != nil {
		return err
	}

	dstore, err := m.open(opts)
	if err != nil {
		return err
	}
	defer dstore.Close()

	// Assuming the user has not modified the blocks namespace
	blocks := namespace.Wrap(dstore, ds.NewKey("/blocks"))

	log.VLog("  - starting CIDv1 to raw multihash block migration")

	// Prepare backing up of CIDs
	backupPath := filepath.Join(opts.Path, backupFile)
	log.VLog("  - backup file will be written to %s", backupPath)
	_, err = os.Stat(backupPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error(err)
			return err
		}
	} else { // backup file exists
		log.Log("WARN: backup file %s already exists. CIDs-Multihash pairs will be appended", backupPath)
	}

	// If it exists, append to it.
	f, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
	if err != nil {
		log.Error(err)
		return err
	}
	defer f.Close()
	buf := bufio.NewWriter(f)
	defer buf.Flush()

	// Will be closed by cidSwapper when it finish writing.
	swapCh := make(chan Swap, 1000)

	writingDone := make(chan struct{})
	go func() {
		for sw := range swapCh {
			fmt.Fprint(buf, sw.Old.String()+","+sw.New.String()+"\n")
		}
		close(writingDone)
	}()

	cidSwapper := CidSwapper{Store: blocks, SwapCh: swapCh}
	total, err := cidSwapper.Run()
	if err != nil {
		log.Error(err)
		return err
	}
	// Wait for our writing to finish before doing the final flush
	// (deferred).
	<-writingDone

	log.Log("%d CIDv1 keys swapped to raw multihashes", total)
	if err := repo.WriteVersion("9"); err != nil {
		log.Error("failed to write version file")
		return err
	}
	log.Log("updated version file")

	return nil
}

// Revert attempts to undo the migration using the log file written by Apply.
func (m Migration) Revert(opts migrate.Options) error {
	log.Verbose = opts.Verbose
	log.Log("reverting %s repo migration", m.Versions())

	lk, err := m.lock(opts)
	if err != nil {
		return err
	}
	defer lk.Close()

	repo := mfsr.RepoPath(opts.Path)

	log.VLog("  - verifying version is '9'")
	if err := repo.CheckVersion("9"); err != nil {
		return err
	}

	log.VLog("  - starting raw multihash to CIDv1 block migration")
	dstore, err := m.open(opts)
	if err != nil {
		return err
	}
	defer dstore.Close()
	blocks := namespace.Wrap(dstore, ds.NewKey("/blocks"))

	// Open revert path for reading
	backupPath := filepath.Join(opts.Path, backupFile)
	log.VLog("  - backup file will be read from %s", backupPath)
	f, err := os.Open(backupPath)
	if err != nil {
		log.Error(err)
		return err
	}

	unswapCh := make(chan Swap, 1000)
	scanner := bufio.NewScanner(f)

	go func() {
		defer close(unswapCh)

		for scanner.Scan() {
			line := scanner.Text()
			oldAndNew := strings.Split(line, ",")
			if len(oldAndNew) != 2 {
				log.Error("bad line in backup file: %s", line)
				continue
			}
			sw := Swap{Old: ds.NewKey(oldAndNew[0]), New: ds.NewKey(oldAndNew[1])}
			unswapCh <- sw
		}
		if err := scanner.Err(); err != nil {
			log.Error(err)
			return
		}

	}()

	cidSwapper := CidSwapper{Store: blocks}
	total, err := cidSwapper.Revert(unswapCh)
	if err != nil {
		log.Error(err)
		return err
	}

	log.Log("%d multihashes reverted to CidV1s", total)
	if err := repo.WriteVersion("8"); err != nil {
		log.Error("failed to write version file")
		return err
	}

	log.Log("reverted version file to version 8")
	err = f.Close()
	if err != nil {
		log.Error("could not close backup file")
		return err
	}
	err = os.Remove(backupPath)
	if err != nil {
		log.Error("could not remove the backup file, but migration worked: %s", err)
	}
	return nil
}