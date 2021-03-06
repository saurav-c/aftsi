import pandas as pd
import sys
import os
import json

def main():
    args = sys.argv
    if len(args) < 2:
        print("Need path to stats")
        return
    path = args[1]
    save = False
    if len(args) > 2 and 'save' in args[2]:
        save = True

    sa = StatAnalyzer(path, save)
    sa.load()


class StatAnalyzer:
    def __init__(self, path, toSave):
        self.path = path
        self.toSave = toSave
        self.stats = []

    def load(self):
        for filename in os.listdir(self.path):
            print(filename)
            split = filename.split('_')
            nodeType = 'TXN' if split[0] == 'txn-manager' else 'KEY'
            addr = split[1]
            with open(os.path.join(self.path, filename), 'r') as f:
                data = f.read()
                data = data[:-2] + ']'
                jsonData = json.loads(data)
                self.parse(jsonData, nodeType, addr)
        self.init_df()
        print('done')

    def parse(self, data, node, addr):
        for batch in data:
            for msg, values in batch.items():
                for val in values['latencies']:
                    record = {
                        'tid': val['tid'],
                        'message': msg,
                        'latency': val['value'],
                        'type': node,
                        'address': addr
                    }
                    self.stats.append(record)

    def init_df(self):
        df = pd.DataFrame(self.stats)
        self.df = df
        if self.toSave:
            self.df.to_csv('stats.csv')

if __name__ == '__main__':
    main()
