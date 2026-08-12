// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {Test} from "forge-std/Test.sol";
import {Ownable} from "@openzeppelin/contracts/access/Ownable.sol";
import {Pausable} from "@openzeppelin/contracts/utils/Pausable.sol";

import {AnchorRegistry} from "../AnchorRegistry.sol";

contract AnchorRegistryTest is Test {
    AnchorRegistry internal registry;

    address internal owner = address(0xA11CE);
    address internal stranger = address(0xB0B);

    event RootAnchored(bytes32 indexed root, uint64 leafCount, uint64 timestamp);

    function setUp() public {
        registry = new AnchorRegistry(owner);
    }

    // --- happy path ---------------------------------------------------------

    function test_AnchorRecordsRootAndEmits() public {
        bytes32 root = keccak256("batch-1");

        vm.expectEmit(true, false, false, true);
        emit RootAnchored(root, 5, uint64(block.timestamp));

        vm.prank(owner);
        registry.anchor(root, 5);

        assertEq(registry.anchoredAt(root), uint64(block.timestamp));
        assertEq(registry.leafCount(root), 5);
        assertEq(registry.totalAnchors(), 1);
    }

    function test_UnanchoredRootReadsAsZero() public view {
        assertEq(registry.anchoredAt(keccak256("never")), 0);
        assertEq(registry.totalAnchors(), 0);
    }

    // --- access control -----------------------------------------------------

    function test_OnlyOwnerCanAnchor() public {
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, stranger));
        registry.anchor(keccak256("batch-1"), 1);
    }

    function test_OnlyOwnerCanPause() public {
        vm.prank(stranger);
        vm.expectRevert(abi.encodeWithSelector(Ownable.OwnableUnauthorizedAccount.selector, stranger));
        registry.pause();
    }

    // --- invariants ---------------------------------------------------------

    /// An anchor asserts that a set of certificates existed at a point in time.
    /// Allowing a root to be re-anchored would let that timestamp move, which
    /// would make the assertion worthless.
    function test_RootCannotBeReanchored() public {
        bytes32 root = keccak256("batch-1");

        vm.prank(owner);
        registry.anchor(root, 5);

        uint64 firstTimestamp = registry.anchoredAt(root);
        vm.warp(block.timestamp + 1 days);

        vm.prank(owner);
        vm.expectRevert(abi.encodeWithSelector(AnchorRegistry.RootAlreadyAnchored.selector, root));
        registry.anchor(root, 5);

        assertEq(registry.anchoredAt(root), firstTimestamp, "timestamp must not move");
        assertEq(registry.totalAnchors(), 1, "a rejected anchor must not count");
    }

    function test_RejectsEmptyRoot() public {
        vm.prank(owner);
        vm.expectRevert(AnchorRegistry.EmptyRoot.selector);
        registry.anchor(bytes32(0), 1);
    }

    function test_RejectsEmptyBatch() public {
        vm.prank(owner);
        vm.expectRevert(AnchorRegistry.EmptyBatch.selector);
        registry.anchor(keccak256("batch-1"), 0);
    }

    function testFuzz_TotalAnchorsTracksDistinctRoots(uint8 count) public {
        vm.assume(count > 0 && count < 64);

        for (uint256 i = 0; i < count; i++) {
            vm.prank(owner);
            registry.anchor(keccak256(abi.encode("batch", i)), 1);
        }
        assertEq(registry.totalAnchors(), count);
    }

    // --- pausing ------------------------------------------------------------

    function test_PauseHaltsAnchoring() public {
        vm.prank(owner);
        registry.pause();

        vm.prank(owner);
        vm.expectRevert(Pausable.EnforcedPause.selector);
        registry.anchor(keccak256("batch-1"), 1);
    }

    /// Pausing exists for a suspected key compromise. Existing anchors are
    /// still true, and readers must not lose access to them.
    function test_PauseLeavesVerificationAvailable() public {
        bytes32 leaf = keccak256(abi.encode("leaf"));
        bytes32[] memory proof = new bytes32[](0);

        vm.prank(owner);
        registry.anchor(leaf, 1);

        vm.prank(owner);
        registry.pause();

        (bool anchored, uint64 timestamp) = registry.verify(leaf, leaf, proof);
        assertTrue(anchored);
        assertGt(timestamp, 0);
    }

    function test_UnpauseRestoresAnchoring() public {
        vm.startPrank(owner);
        registry.pause();
        registry.unpause();
        registry.anchor(keccak256("batch-1"), 1);
        vm.stopPrank();

        assertEq(registry.totalAnchors(), 1);
    }

    // --- verification -------------------------------------------------------

    function test_VerifySingleLeafBatch() public {
        // A one-leaf tree roots at its leaf and needs no proof.
        bytes32 leaf = keccak256(abi.encode(keccak256(abi.encode("content", "commitment"))));
        bytes32[] memory proof = new bytes32[](0);

        vm.prank(owner);
        registry.anchor(leaf, 1);

        (bool anchored, uint64 timestamp) = registry.verify(leaf, leaf, proof);
        assertTrue(anchored);
        assertEq(timestamp, uint64(block.timestamp));
    }

    function test_VerifyTwoLeafBatch() public {
        bytes32 a = keccak256("leaf-a");
        bytes32 b = keccak256("leaf-b");
        bytes32 root = _hashPair(a, b);

        vm.prank(owner);
        registry.anchor(root, 2);

        bytes32[] memory proofForA = new bytes32[](1);
        proofForA[0] = b;
        (bool anchoredA,) = registry.verify(root, a, proofForA);
        assertTrue(anchoredA, "leaf a should prove membership");

        bytes32[] memory proofForB = new bytes32[](1);
        proofForB[0] = a;
        (bool anchoredB,) = registry.verify(root, b, proofForB);
        assertTrue(anchoredB, "leaf b should prove membership");
    }

    function test_VerifyRejectsForeignLeaf() public {
        bytes32 a = keccak256("leaf-a");
        bytes32 b = keccak256("leaf-b");
        bytes32 root = _hashPair(a, b);

        vm.prank(owner);
        registry.anchor(root, 2);

        bytes32[] memory proof = new bytes32[](1);
        proof[0] = b;

        (bool anchored,) = registry.verify(root, keccak256("outsider"), proof);
        assertFalse(anchored);
    }

    /// A valid proof against a root nobody anchored proves nothing, and must
    /// not read as a success.
    function test_VerifyRejectsUnanchoredRoot() public view {
        bytes32 a = keccak256("leaf-a");
        bytes32 b = keccak256("leaf-b");

        bytes32[] memory proof = new bytes32[](1);
        proof[0] = b;

        (bool anchored, uint64 timestamp) = registry.verify(_hashPair(a, b), a, proof);
        assertFalse(anchored);
        assertEq(timestamp, 0);
    }

    /// Mirrors the Go side's hashPair: ascending byte order, so a proof does
    /// not need to say which side each sibling sat on.
    function _hashPair(bytes32 a, bytes32 b) private pure returns (bytes32) {
        return a < b ? keccak256(abi.encodePacked(a, b)) : keccak256(abi.encodePacked(b, a));
    }
}
