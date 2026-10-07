package {{.Package}};

import {{.ServiceClass}};

import com.liferay.portal.kernel.service.ServiceWrapper;

import org.osgi.service.component.annotations.Component;

/**
 * @author {{.Author}}
 */
@Component(
	property = {
	},
	service = ServiceWrapper.class
)
public class {{.ClassName}} extends {{simple .ServiceClass}} {

	public {{.ClassName}}() {
		super(null);
	}

}
