"""Exercise release bumping and retries with real, isolated Git repositories."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("release-version.py").resolve()


class ReleaseVersionTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = self.temp.name
        self.git("init", "-q")
        self.git("config", "user.name", "Test")
        self.git("config", "user.email", "test@example.com")
        self.commit()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.root, text=True).strip()

    def commit(self):
        self.git("commit", "-q", "--allow-empty", "-m", "test")

    def compute(self, major=False, minor=False):
        return subprocess.run(
            ["python3", str(SCRIPT)], cwd=self.root, text=True,
            capture_output=True,
            env={**os.environ, "MAJOR": str(major).lower(), "MINOR": str(minor).lower()},
        )

    def test_initial_bumps(self):
        for major, minor, expected in [(False, False, "v0.0.1"),
                                       (False, True, "v0.1.0"),
                                       (True, True, "v1.0.0")]:
            with self.subTest(expected=expected):
                result = self.compute(major, minor)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(f"next={expected}\n", result.stdout)
                self.assertIn(f"prerelease={str(not major).lower()}\n", result.stdout)

    def test_numeric_sort_and_bumps(self):
        self.git("tag", "v1.9.9")
        self.git("tag", "v1.10.2")
        self.git("tag", "v99.0.0-beta.1")
        self.git("tag", "v02.0.0")
        self.commit()
        for major, minor, expected in [(False, False, "v1.10.3"),
                                       (False, True, "v1.11.0"),
                                       (True, True, "v2.0.0")]:
            with self.subTest(expected=expected):
                result = self.compute(major, minor)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout, f"next={expected}\nprerelease=false\n")

    def test_retry_reuses_annotated_tag(self):
        self.git("tag", "-a", "v0.1.0", "-m", "release")
        result = self.compute(minor=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "next=v0.1.0\nprerelease=true\n")

    def test_old_commit_cannot_release(self):
        old = self.git("rev-parse", "HEAD")
        self.commit()
        self.git("tag", "v1.0.0")
        self.git("checkout", "-q", old)
        result = self.compute()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")


if __name__ == "__main__":
    unittest.main()
