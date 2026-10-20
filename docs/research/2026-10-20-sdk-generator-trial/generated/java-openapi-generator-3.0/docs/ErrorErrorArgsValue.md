

# ErrorErrorArgsValue

## oneOf schemas
* [Long](Long.md)
* [String](String.md)

## Example
```java
// Import classes:
import org.opendustpan.client.model.ErrorErrorArgsValue;
import org.opendustpan.client.model.Long;
import org.opendustpan.client.model.String;

public class Example {
    public static void main(String[] args) {
        ErrorErrorArgsValue exampleErrorErrorArgsValue = new ErrorErrorArgsValue();

        // create a new Long
        Long exampleLong = new Long();
        // set ErrorErrorArgsValue to Long
        exampleErrorErrorArgsValue.setActualInstance(exampleLong);
        // to get back the Long set earlier
        Long testLong = (Long) exampleErrorErrorArgsValue.getActualInstance();

        // create a new String
        String exampleString = new String();
        // set ErrorErrorArgsValue to String
        exampleErrorErrorArgsValue.setActualInstance(exampleString);
        // to get back the String set earlier
        String testString = (String) exampleErrorErrorArgsValue.getActualInstance();
    }
}
```


