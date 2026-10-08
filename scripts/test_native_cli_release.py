import os,subprocess,tempfile,unittest,hashlib
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
class NativeCLIRelease(unittest.TestCase):
 def test_portable_configuration_does_not_ship_broken_mac_auth(self):
  cfg=(ROOT/'.goreleaser.yaml').read_text().split('archives:')[0]
  self.assertNotIn('- darwin',cfg)
  self.assertIn('id: passo',cfg)
  self.assertIn('main: ./cmd/passo',cfg)
  self.assertIn('for BINARY in pyxcloud passo',(ROOT/'scripts/install.sh').read_text())
 def test_native_pipeline_gates_publication_on_both_architectures(self):
  workflow=(ROOT/'.github/workflows/releaser.yml').read_text()
  self.assertIn('macos-15-intel',workflow);self.assertIn('macos-15',workflow)
  self.assertIn('needs: [portable, native-macos]',workflow)
  self.assertIn('default: false',workflow)
  self.assertIn('--verify-tag',workflow);self.assertIn('--draft',workflow)
  self.assertIn('scripts/build-native-cli.sh',workflow)
 def test_tag_version_timeouts_and_download_integrity_are_required(self):
  workflow=(ROOT/'.github/workflows/releaser.yml').read_text()
  self.assertIn('CLI_RELEASE_VERSION:',workflow)
  self.assertEqual(workflow.count('timeout-minutes: 15'),3)
  installer=(ROOT/'scripts/install.sh').read_text()
  self.assertIn('checksums.txt',installer)
  self.assertLess(installer.index('checksum mismatch'),installer.index('tar -xzf'))
 def test_linux_release_uses_available_canonical_runner(self):
  workflow=(ROOT/'.github/workflows/releaser.yml').read_text()
  self.assertEqual(workflow.count('runs-on: ubuntu-latest'),2)
  self.assertNotIn('pyxflow',workflow)
 def test_native_builder_refuses_linux_before_go_or_output(self):
  with tempfile.TemporaryDirectory() as tmp:
   d=Path(tmp);fake=d/'uname';fake.write_text('#!/bin/sh\nprintf Linux\n');fake.chmod(0o700)
   r=subprocess.run(['bash',str(ROOT/'scripts/build-native-cli.sh'),'arm64',str(d/'out')],env={**os.environ,'PATH':str(d)+':'+os.environ['PATH']},capture_output=True,text=True)
   self.assertNotEqual(r.returncode,0);self.assertIn('requires macOS',r.stderr);self.assertFalse((d/'out').exists())
 def test_installer_only_extracts_exact_verified_archive(self):
  for valid in [False,True]:
   with self.subTest(valid=valid),tempfile.TemporaryDirectory() as tmp:
    d=Path(tmp);payload=d/'payload';payload.write_bytes(b'owned archive fixture');checksum=d/'checksums';digest=hashlib.sha256(payload.read_bytes()).hexdigest() if valid else '0'*64;checksum.write_text(digest+'  pyxcloud_Darwin_arm64.tar.gz\n');marker=d/'extracted'
    tools={'uname':'if [ "$1" = -s ]; then echo Darwin; else echo arm64; fi','curl':'while [ "$#" -gt 0 ]; do case "$1" in -o) out="$2"; shift 2;; https*) url="$1"; shift;; *) shift;; esac; done; case "$url" in *checksums.txt) cp "$FIXTURE_CHECKSUM" "$out";; *) cp "$FIXTURE_PAYLOAD" "$out";; esac','tar':'touch "$FIXTURE_MARKER"; exit 1'}
    for name,body in tools.items():p=d/name;p.write_text('#!/bin/sh\n'+body+'\n');p.chmod(0o700)
    env={**os.environ,'PATH':str(d)+':'+os.environ['PATH'],'FIXTURE_PAYLOAD':str(payload),'FIXTURE_CHECKSUM':str(checksum),'FIXTURE_MARKER':str(marker)}
    r=subprocess.run(['bash',str(ROOT/'scripts/install.sh')],env=env,capture_output=True,text=True)
    self.assertNotEqual(r.returncode,0);self.assertEqual(marker.exists(),valid)
    if not valid:self.assertIn('checksum mismatch',r.stderr)
if __name__=='__main__':unittest.main()
