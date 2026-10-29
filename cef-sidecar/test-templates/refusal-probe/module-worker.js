// A module worker whose import is refused never runs this.
import 'http://127.0.0.1:18081/module-import.js';
postMessage(self.leaked ? 'reached' : 'refused');
