class EventEmitter {
  on(event, handler) {
    this.register(event, handler);
  }

  emit(event, data) {
    this.dispatch(event, data);
  }

  register(event, handler) {}
  dispatch(event, data) {}
}

class Logger extends EventEmitter {
  log(message) {
    this.emit("log", message);
    console.log(message);
  }
}

const DEFAULT_LEVEL = "info";

const createLogger = (level) => {
  const logger = new Logger();
  logger.log("initialized");
  return logger;
};

const processEvents = function(emitter) {
  emitter.on("data", handleData);
};

function handleData(data) {
  transform(data);
}

function transform(data) {
  return data;
}
