package store

import (
	storetypes "github.com/cosmos/cosmos-sdk/store/types"
)

func CommitHash(version int64, storeRoots map[string][]byte) []byte {
	infos := make([]storetypes.StoreInfo, 0, len(storeRoots))
	for name, root := range storeRoots {
		infos = append(infos, storetypes.StoreInfo{
			Name: name,
			CommitId: storetypes.CommitID{
				Version: version,
				Hash:    root,
			},
		})
	}

	commitInfo := storetypes.CommitInfo{
		Version:    version,
		StoreInfos: infos,
	}

	return commitInfo.Hash()
}
