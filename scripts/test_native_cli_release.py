import os,subprocess,tempfile,unittest
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
 def test_native_builder_refuses_linux_before_go_or_output(self):
  with tempfile.TemporaryDirectory() as tmp:
   d=Path(tmp);fake=d/'uname';fake.write_text('#!/bin/sh\nprintf Linux\n');fake.chmod(0o700)
   r=subprocess.run(['bash',str(ROOT/'scripts/build-native-cli.sh'),'arm64',str(d/'out')],env={**os.environ,'PATH':str(d)+':'+os.environ['PATH']},capture_output=True,text=True)
   self.assertNotEqual(r.returncode,0);self.assertIn('requires macOS',r.stderr);self.assertFalse((d/'out').exists())
if __name__=='__main__':unittest.main()
