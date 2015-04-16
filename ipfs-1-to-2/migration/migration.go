package mg1

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	migrate "github.com/ipfs/fs-repo-migrations/go-migrate"
	dstore "github.com/ipfs/fs-repo-migrations/ipfs-1-to-2/go-datastore"
	flatfs "github.com/ipfs/fs-repo-migrations/ipfs-1-to-2/go-datastore/flatfs"
	leveldb "github.com/ipfs/fs-repo-migrations/ipfs-1-to-2/go-datastore/leveldb"
	dsq "github.com/ipfs/fs-repo-migrations/ipfs-1-to-2/go-datastore/query"
	mfsr "github.com/ipfs/fs-repo-migrations/mfsr"
)

const peerKeyName = "peer.key"

type Migration struct{}

func (m Migration) Versions() string {
	return "1-to-2"
}

func (m Migration) Reversible() bool {
	return true
}

func (m Migration) Apply(opts migrate.Options) error {
	repo := mfsr.RepoPath(opts.Path)

	if err := repo.CheckVersion("1"); err != nil {
		return err
	}

	// 1) run some sanity checks to make sure we should even bother
	err := sanityChecks(opts)
	if err != nil {
		return err
	}

	// 2) Transfer blocks out of leveldb into flatDB
	err = transferBlocksToFlatDB(opts.Path)
	if err != nil {
		return err
	}

	// 3) move ipfs path from .go-ipfs to .ipfs
	newpath, err := moveIpfsDir(opts.Path)
	if err != nil {
		return err
	}

	// 4) Update version number
	repo = mfsr.RepoPath(newpath)
	err = repo.WriteVersion("2")
	if err != nil {
		return err
	}

	return nil
}

func (m Migration) Revert(opts migrate.Options) error {
	repo := mfsr.RepoPath(opts.Path)
	if err := repo.CheckVersion("2"); err != nil {
		return err
	}

	// 1) Move directory back to .go-ipfs
	npath, err := reverseIpfsDir(opts.Path)
	if err != nil {
		return err
	}

	// 2) move blocks back from flatfs to leveldb
	err = transferBlocksFromFlatDB(npath)
	if err != nil {
		return err
	}

	// 3) change version number back down
	repo = mfsr.RepoPath(npath)
	err = repo.WriteVersion("1")
	if err != nil {
		return err
	}

	return nil
}

// sanityChecks performs a set of tests to make sure the Migration will go
// smoothly
func sanityChecks(opts migrate.Options) error {
	npath := strings.Replace(opts.Path, ".go-ipfs", ".ipfs", 1)

	// make sure we can move the repo from .go-ipfs to .ipfs
	err := os.Mkdir(npath, 0777)
	if err != nil {
		return err
	}

	// we can? good, remove it now
	err = os.Remove(npath)
	if err != nil {
		// this is weird... not worth continuing
		return err
	}

	return nil
}

func transferBlocksToFlatDB(repopath string) error {
	ldbpath := path.Join(repopath, "datastore")
	ldb, err := leveldb.NewDatastore(ldbpath, nil)
	if err != nil {
		return err
	}

	blockspath := path.Join(repopath, "blocks")
	err = os.Mkdir(blockspath, 0777)
	if err != nil && !os.IsExist(err) {
		return err
	}

	fds, err := flatfs.New(blockspath, 4)
	if err != nil {
		return err
	}

	return transferBlocks(ldb, fds, "/b/", "")
}

func transferBlocksFromFlatDB(repopath string) error {

	ldbpath := path.Join(repopath, "datastore")
	blockspath := path.Join(repopath, "blocks")
	fds, err := flatfs.New(blockspath, 4)
	if err != nil {
		return err
	}

	ldb, err := leveldb.NewDatastore(ldbpath, nil)
	if err != nil {
		return err
	}

	err = transferBlocks(fds, ldb, "", "/b/")
	if err != nil {
		return err
	}

	// Now remove the blocks directory
	err = os.RemoveAll(blockspath)
	if err != nil {
		return err
	}

	return nil
}

func transferBlocks(from, to dstore.Datastore, fpref, tpref string) error {
	q := dsq.Query{Prefix: fpref, KeysOnly: true}
	res, err := from.Query(q)
	if err != nil {
		return err
	}

	for result := range res.Next() {
		nkey := fmt.Sprintf("%s%s", tpref, result.Key[len(fpref):])

		fkey := dstore.NewKey(result.Key)
		val, err := from.Get(fkey)
		if err != nil {
			return err
		}

		err = to.Put(dstore.NewKey(nkey), val)
		if err != nil {
			return err
		}

		err = from.Delete(fkey)
		if err != nil {
			return err
		}
	}

	return nil
}

func moveIpfsDir(curpath string) (string, error) {
	newpath := strings.Replace(curpath, ".go-ipfs", ".ipfs", 1)
	return newpath, os.Rename(curpath, newpath)
}

func reverseIpfsDir(curpath string) (string, error) {
	newpath := strings.Replace(curpath, ".ipfs", ".go-ipfs", 1)
	return newpath, os.Rename(curpath, newpath)
}

func loadConfigJSON(repoPath string) (map[string]interface{}, error) {
	cfgPath := path.Join(repoPath, "config")
	fi, err := os.Open(cfgPath)
	if err != nil {
		return nil, err
	}

	var out map[string]interface{}
	err = json.NewDecoder(fi).Decode(&out)
	if err != nil {
		return nil, err
	}

	return out, nil
}

func saveConfigJSON(repoPath string, cfg map[string]interface{}) error {
	cfgPath := path.Join(repoPath, "config")
	fi, err := os.Create(cfgPath)
	if err != nil {
		return err
	}

	out, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return err
	}

	_, err = fi.Write(out)
	if err != nil {
		return err
	}

	return nil
}