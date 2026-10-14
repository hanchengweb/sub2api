"""Fetch the pinned public weights before starting the offline inference worker."""
import argparse
import hashlib
import json
import pathlib
import time
import urllib.request


def digest(path):
    result = hashlib.sha256()
    with path.open('rb') as source:
        while chunk := source.read(1024 * 1024):
            result.update(chunk)
    return result.hexdigest()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--directory', type=pathlib.Path, default=pathlib.Path(__file__).parent / 'models')
    parser.add_argument('--mirror', choices=['huggingface', 'modelscope'], default='huggingface')
    parser.add_argument('--verify-only', action='store_true')
    args = parser.parse_args()
    lock = json.loads(pathlib.Path(__file__).with_name('model-lock.json').read_text(encoding='utf-8'))
    for item in lock['files']:
        # The model card is documentation, not an inference input. The mirror's
        # card can differ; all actual model files must match the pinned hashes.
        if item['path'] == 'README.md':
            continue
        relative = pathlib.PurePosixPath(item['path'])
        if relative.is_absolute() or '..' in relative.parts or not item.get('sha256'):
            raise ValueError('invalid model lock')
        path = args.directory.joinpath(*relative.parts)
        if path.is_file() and path.stat().st_size == item['bytes'] and digest(path) == item['sha256']:
            print('verified', item['path'], flush=True)
            continue
        if args.verify_only:
            raise ValueError('model integrity mismatch: ' + item['path'])
        path.parent.mkdir(parents=True, exist_ok=True)
        url = (f"https://huggingface.co/{lock['repo']}/resolve/{lock['revision']}/" if args.mirror == 'huggingface'
               else f"https://modelscope.cn/models/{lock['repo']}/resolve/master/") + item['path']
        temporary = path.with_suffix(path.suffix + '.partial')
        for attempt in range(3):
            try:
                with urllib.request.urlopen(url, timeout=45) as response, temporary.open('wb') as output:
                    while chunk := response.read(1024 * 1024):
                        output.write(chunk)
                if temporary.stat().st_size != item['bytes'] or digest(temporary) != item['sha256']:
                    raise ValueError('download integrity mismatch: ' + item['path'])
                temporary.replace(path)
                print('verified', item['path'], flush=True)
                break
            except Exception:
                if attempt == 2:
                    raise
                time.sleep(2)


if __name__ == '__main__':
    main()
