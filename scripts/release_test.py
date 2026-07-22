from __future__ import annotations

from contextlib import redirect_stderr
import io
from pathlib import Path
import tarfile
import tempfile
import unittest
import zipfile

from scripts.release import archive_name, sha256_file, validate_version, write_archive


class ReleaseTest(unittest.TestCase):
    def test_archive_names_are_versioned(self) -> None:
        self.assertEqual(
            archive_name("0.1.1", "darwin", "arm64"),
            "nvoken_0.1.1_darwin_arm64.tar.gz",
        )
        self.assertEqual(
            archive_name("0.1.1", "windows", "amd64"),
            "nvoken_0.1.1_windows_amd64.zip",
        )

    def test_version_validation(self) -> None:
        self.assertEqual(validate_version("1.2.3"), "1.2.3")
        self.assertEqual(validate_version("1.2.3-rc.1"), "1.2.3-rc.1")
        for invalid in ("v1.2.3", "1.2", "01.2.3", "latest"):
            with self.subTest(invalid=invalid), redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit):
                    validate_version(invalid)

    def test_archives_contain_both_binaries_and_license(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            files = []
            for name in ("nvoken", "nvokend", "LICENSE"):
                path = root / name
                path.write_text(name, encoding="utf-8")
                path.chmod(0o755 if name != "LICENSE" else 0o644)
                files.append((path, name))

            tar_path = root / "release.tar.gz"
            write_archive(tar_path, files)
            with tarfile.open(tar_path, "r:gz") as archive:
                self.assertEqual(
                    set(archive.getnames()),
                    {"nvoken", "nvokend", "LICENSE"},
                )
                self.assertEqual(archive.getmember("nvoken").mode, 0o755)
                self.assertEqual(archive.getmember("LICENSE").mode, 0o644)

            zip_path = root / "release.zip"
            write_archive(zip_path, files)
            with zipfile.ZipFile(zip_path) as archive:
                self.assertEqual(
                    set(archive.namelist()),
                    {"nvoken", "nvokend", "LICENSE"},
                )
                self.assertEqual(archive.getinfo("nvoken").external_attr >> 16, 0o755)
                self.assertEqual(archive.getinfo("LICENSE").external_attr >> 16, 0o644)

    def test_checksum_uses_file_bytes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "asset"
            path.write_bytes(b"nvoken")
            self.assertEqual(
                sha256_file(path),
                "9992a3b0a07f1238ed6bb473b7daa90675ca03620d2a153843817b2b6fecc5cc",
            )

    def test_archive_metadata_is_reproducible(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "nvoken"
            binary.write_bytes(b"binary")
            binary.chmod(0o755)
            files = [(binary, "nvoken")]
            for extension in ("tar.gz", "zip"):
                first = root / f"first.{extension}"
                second = root / f"second.{extension}"
                write_archive(first, files)
                write_archive(second, files)
                self.assertEqual(first.read_bytes(), second.read_bytes())


if __name__ == "__main__":
    unittest.main()
