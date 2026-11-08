"""Copy and mention-safety regression tests for off-schedule announcements."""
import unittest
from game2_messages import UNSCHEDULED_NEXT_GAME_TEXT, unscheduled_next_game_payload


class UnscheduledAnnouncementTests(unittest.TestCase):
    def test_approved_copy(self):
        self.assertEqual(
            UNSCHEDULED_NEXT_GAME_TEXT,
            "🦈 **Another game of Blood on the Clocktower?**\n\n"
            "The next game is being organised!\n\n"
            "If you're around and fancy joining another game, come along!",
        )
        self.assertNotIn("Game 2 is being organised", UNSCHEDULED_NEXT_GAME_TEXT)

    def test_never_pings_any_role_or_everyone(self):
        payload = unscheduled_next_game_payload()
        self.assertEqual(payload["allowed_mentions"], {"parse": []})
        self.assertEqual(payload["content"], UNSCHEDULED_NEXT_GAME_TEXT)
        for forbidden in ("<@", "@everyone", "@here", "@Game 2"):
            self.assertNotIn(forbidden, payload["content"])

    def test_no_shared_mutable_mentions(self):
        first = unscheduled_next_game_payload()
        first["allowed_mentions"]["parse"].append("roles")
        self.assertEqual(unscheduled_next_game_payload()["allowed_mentions"], {"parse": []})


if __name__ == "__main__":
    unittest.main()
