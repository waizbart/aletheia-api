// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";
import {MerkleProof} from "@openzeppelin/contracts/utils/cryptography/MerkleProof.sol";

/// @title AnchorRegistry
/// @notice Public, append-only record of Merkle roots, each committing a batch
///         of Aletheia certificates.
///
/// @dev The previous design sent raw calldata to an address with no contract
///      behind it, so nothing was readable on chain and nothing could be
///      verified against it. Here each batch becomes an event and a stored
///      timestamp, and anyone can check a certificate's inclusion proof with
///      {verify} without trusting the API that issued it.
///
///      Roots are never removed or overwritten. An anchor is a claim that a set
///      of certificates existed at a point in time; letting it be edited would
///      make the claim worthless.
contract AnchorRegistry is Ownable, Pausable {
    /// @notice Block timestamp at which a root was anchored. Zero means never.
    mapping(bytes32 root => uint64 anchoredAt) public anchoredAt;

    /// @notice Number of certificates committed under a root.
    mapping(bytes32 root => uint64 count) public leafCount;

    /// @notice Total roots anchored, so an indexer can detect gaps.
    uint64 public totalAnchors;

    event RootAnchored(bytes32 indexed root, uint64 leafCount, uint64 timestamp);

    error EmptyRoot();
    error EmptyBatch();
    error RootAlreadyAnchored(bytes32 root);

    constructor(address initialOwner) Ownable(initialOwner) {}

    /// @notice Commit a batch of certificates.
    /// @param root  Merkle root over the batch's leaves.
    /// @param count Number of certificates the root commits.
    ///
    /// @dev Restricted to the owner: the registry's value comes from the fact
    ///      that every root in it was published by the operator whose
    ///      certificates it describes. An open write would let anyone flood it
    ///      with roots that mean nothing.
    function anchor(bytes32 root, uint64 count) external onlyOwner whenNotPaused {
        if (root == bytes32(0)) revert EmptyRoot();
        if (count == 0) revert EmptyBatch();
        if (anchoredAt[root] != 0) revert RootAlreadyAnchored(root);

        // Checks above, effects here, no external calls at all: there is no
        // reentrancy surface to protect.
        anchoredAt[root] = uint64(block.timestamp);
        leafCount[root] = count;
        unchecked {
            // A uint64 counter cannot realistically overflow at one increment
            // per batch, and wrapping it would only affect an off-chain gap
            // check.
            ++totalAnchors;
        }

        emit RootAnchored(root, count, uint64(block.timestamp));
    }

    /// @notice Check a certificate's inclusion in an anchored batch.
    /// @param root  The anchored Merkle root.
    /// @param leaf  keccak256(keccak256(contentHash, featureCommitment)).
    /// @param proof Sibling hashes from the leaf upward.
    /// @return anchored Whether the proof is valid for a root this registry has.
    /// @return timestamp When the root was anchored, or zero if it was not.
    function verify(bytes32 root, bytes32 leaf, bytes32[] calldata proof)
        external
        view
        returns (bool anchored, uint64 timestamp)
    {
        timestamp = anchoredAt[root];
        if (timestamp == 0) {
            return (false, 0);
        }
        return (MerkleProof.verifyCalldata(proof, root, leaf), timestamp);
    }

    /// @notice Halt anchoring, for use if the anchoring key is suspected
    ///         compromised. Verification stays available: existing anchors are
    ///         still true, and readers must not lose access to them.
    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }
}
