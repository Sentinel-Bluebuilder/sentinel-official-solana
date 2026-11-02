package store

const NodeKeyPrefix = nodeKeyPrefix

func HashInner(height, size, version int64, key, left, right []byte) []byte {
	return hashInner(&node{height: height, size: size, version: version, key: key}, left, right)
}

func HashLeaf(size, version int64, key, value []byte) []byte {
	return hashLeaf(&node{size: size, version: version, key: key, value: value, isLeaf: true})
}

func ParseNode(raw []byte) (size, version int64, key, value []byte, isLeaf bool, err error) {
	n, err := parseNode(raw)
	if err != nil {
		return 0, 0, nil, nil, false, err
	}

	return n.size, n.version, n.key, n.value, n.isLeaf, nil
}
