export namespace main {
	
	export class CalStepDTO {
	    kind: string;
	    index: number;
	    label: string;
	    prompt: string;
	
	    static createFrom(source: any = {}) {
	        return new CalStepDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.index = source["index"];
	        this.label = source["label"];
	        this.prompt = source["prompt"];
	    }
	}
	export class ConnectionInfo {
	    configPath: string;
	    port: string;
	    bars: number;
	    lcs: number;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.configPath = source["configPath"];
	        this.port = source["port"];
	        this.bars = source["bars"];
	        this.lcs = source["lcs"];
	    }
	}

}

